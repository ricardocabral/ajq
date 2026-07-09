package provision

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// downloadTimeout bounds a single asset download when the caller-provided
// context has no deadline. Model files are large, so this is generous.
const downloadTimeout = 30 * time.Minute

// Progress reports the state of a single asset installation. It is emitted
// repeatedly during a download and once more with Done=true when the asset is
// fully installed (or immediately for an already-present asset).
type Progress struct {
	// Asset is the logical name of the asset being installed.
	Asset string
	// Kind is engine or model.
	Kind Kind
	// BytesDone is the number of bytes downloaded so far.
	BytesDone int64
	// BytesTotal is the expected total, or 0 when unknown.
	BytesTotal int64
	// Done is true on the final event for the asset.
	Done bool
	// Skipped is true when the asset was already present (no download).
	Skipped bool
}

// ProgressFunc receives Progress events. It must not block for long. A nil
// ProgressFunc disables reporting. This callback type keeps core provisioning
// logic decoupled from any specific CLI/UI framework.
type ProgressFunc func(Progress)

// Install fulfills a Plan by downloading and verifying every missing asset. It
// is a no-op for assets already present (reported via a Skipped progress
// event). Downloads are atomic: bytes are written to a temp file, checksummed,
// and only renamed into place on a verified match; partial temp files are
// always removed. Provisioning metadata is updated after each successful
// install. Install never mutates already-present assets.
func (pr *Provisioner) Install(ctx context.Context, plan Plan, progress ProgressFunc) error {
	if progress == nil {
		progress = func(Progress) {}
	}

	metadata, err := LoadMetadata(plan.Layout.MetadataPath())
	if err != nil {
		return err
	}

	engineInstalledNew := !plan.Engine.Present && plan.Engine.Asset.BundleDownloadable()
	if plan.Engine.Present && plan.Engine.Source == "bundle" {
		progress(Progress{Asset: plan.Engine.Asset.Name, Kind: plan.Engine.Asset.Kind, Done: true, Skipped: true})
	} else if err := pr.installOne(ctx, plan.Layout, plan.Engine, progress, func(a InstalledAsset) {
		metadata.SetEngine(a)
	}); err != nil {
		return err
	}
	if err := metadata.Save(plan.Layout.MetadataPath()); err != nil {
		if engineInstalledNew {
			_ = os.RemoveAll(plan.Layout.EngineBundleDir(plan.Engine.Asset.ReleaseTag))
		}
		return err
	}

	return pr.installModelWithMetadata(ctx, plan, progress, metadata)
}

// InstallModel fulfills only the model portion of a Plan. It is used by model
// management commands so `ajq models pull <name>` does not provision an engine.
func (pr *Provisioner) InstallModel(ctx context.Context, plan Plan, progress ProgressFunc) error {
	if progress == nil {
		progress = func(Progress) {}
	}
	metadata, err := LoadMetadata(plan.Layout.MetadataPath())
	if err != nil {
		return err
	}
	return pr.installModelWithMetadata(ctx, plan, progress, metadata)
}

func (pr *Provisioner) installModelWithMetadata(ctx context.Context, plan Plan, progress ProgressFunc, metadata Metadata) error {
	if err := pr.installOne(ctx, plan.Layout, plan.Model, progress, func(a InstalledAsset) {
		metadata.SetModel(a)
	}); err != nil {
		return err
	}
	return metadata.Save(plan.Layout.MetadataPath())
}

// installOne installs a single asset if it is missing. When present it emits a
// Skipped progress event and records the discovered location in metadata so the
// on-disk record reflects reality even for locally-provided assets.
func (pr *Provisioner) installOne(ctx context.Context, layout Layout, status AssetStatus, progress ProgressFunc, record func(InstalledAsset)) error {
	asset := status.Asset
	if status.Present {
		progress(Progress{Asset: asset.Name, Kind: asset.Kind, Done: true, Skipped: true})
		if info, err := os.Stat(status.Path); err == nil {
			record(InstalledAsset{
				Name:        asset.Name,
				Version:     asset.Version,
				Path:        status.Path,
				SHA256:      asset.SHA256,
				Size:        info.Size(),
				InstalledAt: time.Now().UTC(),
			})
		}
		return nil
	}

	if asset.BundleDownloadable() {
		installed, err := pr.installEngineBundle(ctx, layout, asset, progress)
		if err != nil {
			return err
		}
		record(installed)
		return nil
	}

	if !asset.Downloadable() {
		return fmt.Errorf("cannot provision %s %q: no download source configured (URL/checksum missing); install it manually or set an override", asset.Kind, asset.Name)
	}

	sum, size, err := pr.download(ctx, layout, asset, progress)
	if err != nil {
		return err
	}
	record(InstalledAsset{
		Name:        asset.Name,
		Version:     asset.Version,
		Path:        status.Path,
		SHA256:      sum,
		Size:        size,
		InstalledAt: time.Now().UTC(),
	})
	// Move verified temp file into final destination handled inside download.
	progress(Progress{Asset: asset.Name, Kind: asset.Kind, BytesDone: size, BytesTotal: size, Done: true})
	return nil
}

// download fetches an asset to a temp file, verifies its checksum, and renames
// it atomically into the destination. It returns the verified checksum and
// byte size. Any error removes the temp file so no partial artifact remains.
func (pr *Provisioner) downloadToTemp(ctx context.Context, layout Layout, asset Asset, progress ProgressFunc) (string, string, int64, func(), error) {
	if err := os.MkdirAll(layout.TempDir(), 0o700); err != nil {
		return "", "", 0, nil, fmt.Errorf("create temp dir: %w", err)
	}
	tmp, err := os.CreateTemp(layout.TempDir(), "*.part-"+asset.Filename)
	if err != nil {
		return "", "", 0, nil, fmt.Errorf("create temp download file: %w", err)
	}
	tmpName := tmp.Name()
	cleanup := func() {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
	}

	reqCtx := ctx
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		reqCtx, cancel = context.WithTimeout(ctx, downloadTimeout)
		defer cancel()
	}
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, asset.URL, nil)
	if err != nil {
		cleanup()
		return "", "", 0, nil, fmt.Errorf("build download request: %w", err)
	}
	resp, err := pr.httpClient().Do(req)
	if err != nil {
		cleanup()
		return "", "", 0, nil, fmt.Errorf("download %s: %w", asset.Name, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		cleanup()
		return "", "", 0, nil, fmt.Errorf("download %s: server returned status %d", asset.Name, resp.StatusCode)
	}

	total := asset.Size
	if resp.ContentLength > 0 {
		total = resp.ContentLength
	}
	hasher := sha256.New()
	counter := &progressWriter{asset: asset, total: total, progress: progress}
	written, err := io.Copy(io.MultiWriter(tmp, hasher, counter), resp.Body)
	if err != nil {
		cleanup()
		return "", "", 0, nil, fmt.Errorf("download %s: %w", asset.Name, err)
	}
	if err := tmp.Sync(); err != nil {
		cleanup()
		return "", "", 0, nil, fmt.Errorf("flush download: %w", err)
	}
	sum := hex.EncodeToString(hasher.Sum(nil))
	if !strings.EqualFold(sum, asset.SHA256) {
		cleanup()
		return "", "", 0, nil, fmt.Errorf("checksum mismatch for %s: got %s, want %s", asset.Name, sum, asset.SHA256)
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return "", "", 0, nil, fmt.Errorf("close download: %w", err)
	}
	return tmpName, sum, written, cleanup, nil
}

func (pr *Provisioner) installEngineBundle(ctx context.Context, layout Layout, asset Asset, progress ProgressFunc) (InstalledAsset, error) {
	archivePath, sum, size, cleanupArchive, err := pr.downloadToTemp(ctx, layout, asset, progress)
	if err != nil {
		return InstalledAsset{}, err
	}
	defer cleanupArchive()

	if err := os.MkdirAll(layout.TempDir(), 0o700); err != nil {
		return InstalledAsset{}, fmt.Errorf("create temp dir: %w", err)
	}
	extractRoot, err := os.MkdirTemp(layout.TempDir(), asset.ReleaseTag+".extract-*")
	if err != nil {
		return InstalledAsset{}, fmt.Errorf("create temp extraction dir: %w", err)
	}
	cleanupExtract := true
	defer func() {
		if cleanupExtract {
			_ = os.RemoveAll(extractRoot)
		}
	}()

	files, err := ExtractArchive(archivePath, extractRoot, 0)
	if err != nil {
		return InstalledAsset{}, err
	}
	binaryTmp := filepath.Join(extractRoot, filepath.FromSlash(asset.BinaryPath))
	if !pathWithin(extractRoot, binaryTmp) {
		return InstalledAsset{}, fmt.Errorf("bundle binary path %q escapes extracted root", asset.BinaryPath)
	}
	if !executableFile(binaryTmp) {
		return InstalledAsset{}, fmt.Errorf("bundle binary %q is missing or not executable", asset.BinaryPath)
	}

	finalRoot := layout.EngineBundleDir(asset.ReleaseTag)
	if err := os.MkdirAll(filepath.Dir(finalRoot), 0o700); err != nil {
		return InstalledAsset{}, fmt.Errorf("create engine bundle parent: %w", err)
	}
	if _, err := os.Stat(finalRoot); err == nil {
		return InstalledAsset{}, fmt.Errorf("engine bundle destination %q already exists but is not a valid cache hit", finalRoot)
	} else if err != nil && !os.IsNotExist(err) {
		return InstalledAsset{}, fmt.Errorf("stat engine bundle destination: %w", err)
	}
	if err := os.Rename(extractRoot, finalRoot); err != nil {
		return InstalledAsset{}, fmt.Errorf("install engine bundle into place: %w", err)
	}
	cleanupExtract = false

	binaryFinal := layout.EngineBundleBinaryPath(asset.ReleaseTag, asset.BinaryPath)
	progress(Progress{Asset: asset.Name, Kind: asset.Kind, BytesDone: size, BytesTotal: size, Done: true})
	return InstalledAsset{
		Name:        asset.Name,
		Version:     asset.ReleaseTag,
		Path:        binaryFinal,
		SHA256:      sum,
		Size:        size,
		InstalledAt: time.Now().UTC(),
		Bundle: &InstalledBundle{
			Tag:           asset.ReleaseTag,
			Root:          finalRoot,
			Files:         files,
			BinaryPath:    binaryFinal,
			BinaryRelPath: asset.BinaryPath,
			ArchiveSHA256: sum,
			ArchiveSize:   size,
		},
	}, nil
}

func (pr *Provisioner) download(ctx context.Context, layout Layout, asset Asset, progress ProgressFunc) (string, int64, error) {
	dest := destinationFor(layout, asset)
	if err := os.MkdirAll(filepath.Dir(dest), 0o700); err != nil {
		return "", 0, fmt.Errorf("create destination dir: %w", err)
	}
	if err := os.MkdirAll(layout.TempDir(), 0o700); err != nil {
		return "", 0, fmt.Errorf("create temp dir: %w", err)
	}

	tmp, err := os.CreateTemp(layout.TempDir(), "*.part-"+asset.Filename)
	if err != nil {
		return "", 0, fmt.Errorf("create temp download file: %w", err)
	}
	tmpName := tmp.Name()
	// Ensure the temp file is removed on every error path; a successful rename
	// makes this remove a no-op.
	cleanup := true
	defer func() {
		_ = tmp.Close()
		if cleanup {
			_ = os.Remove(tmpName)
		}
	}()

	reqCtx := ctx
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		reqCtx, cancel = context.WithTimeout(ctx, downloadTimeout)
		defer cancel()
	}

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, asset.URL, nil)
	if err != nil {
		return "", 0, fmt.Errorf("build download request: %w", err)
	}
	resp, err := pr.httpClient().Do(req)
	if err != nil {
		return "", 0, fmt.Errorf("download %s: %w", asset.Name, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", 0, fmt.Errorf("download %s: server returned status %d", asset.Name, resp.StatusCode)
	}

	total := asset.Size
	if resp.ContentLength > 0 {
		total = resp.ContentLength
	}

	hasher := sha256.New()
	counter := &progressWriter{
		asset:    asset,
		total:    total,
		progress: progress,
	}
	written, err := io.Copy(io.MultiWriter(tmp, hasher, counter), resp.Body)
	if err != nil {
		return "", 0, fmt.Errorf("download %s: %w", asset.Name, err)
	}
	if err := tmp.Sync(); err != nil {
		return "", 0, fmt.Errorf("flush download: %w", err)
	}

	sum := hex.EncodeToString(hasher.Sum(nil))
	if !strings.EqualFold(sum, asset.SHA256) {
		return "", 0, fmt.Errorf("checksum mismatch for %s: got %s, want %s", asset.Name, sum, asset.SHA256)
	}
	if err := tmp.Close(); err != nil {
		return "", 0, fmt.Errorf("close download: %w", err)
	}

	// Engine binaries must be executable.
	mode := os.FileMode(0o644)
	if asset.Kind == KindEngine {
		mode = 0o755
	}
	if err := os.Chmod(tmpName, mode); err != nil {
		return "", 0, fmt.Errorf("set permissions: %w", err)
	}
	if err := os.Rename(tmpName, dest); err != nil {
		return "", 0, fmt.Errorf("install %s into place: %w", asset.Name, err)
	}
	cleanup = false
	return sum, written, nil
}

// destinationFor returns the final install path for an asset within the layout.
func destinationFor(layout Layout, asset Asset) string {
	if asset.BundleDownloadable() {
		return layout.EngineBundleBinaryPath(asset.ReleaseTag, asset.BinaryPath)
	}
	if asset.Kind == KindEngine {
		return layout.EngineBinaryPath(asset.Filename)
	}
	return layout.ModelPath(asset.Filename)
}

// httpClient returns the injected client or a bounded default.
func (pr *Provisioner) httpClient() *http.Client {
	if pr.HTTPClient != nil {
		return pr.HTTPClient
	}
	return &http.Client{Timeout: downloadTimeout}
}

// progressWriter counts bytes and emits Progress events as data streams in.
type progressWriter struct {
	asset    Asset
	total    int64
	done     int64
	progress ProgressFunc
}

func (w *progressWriter) Write(p []byte) (int, error) {
	n := len(p)
	w.done += int64(n)
	w.progress(Progress{
		Asset:      w.asset.Name,
		Kind:       w.asset.Kind,
		BytesDone:  w.done,
		BytesTotal: w.total,
	})
	return n, nil
}
