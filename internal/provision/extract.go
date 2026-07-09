package provision

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	slashpath "path"
	"path/filepath"
	"strings"
)

const defaultExtractMaxBytes int64 = 1 << 30 // 1 GiB decompressed regular-file data.

// ExtractArchive extracts a .tar.gz/.tgz or .zip archive into dest and returns
// the regular files written, relative to dest using slash-separated paths. It is
// deliberately strict because engine archives are downloaded from the network:
// archive names must be relative clean slash paths, symlinks/hardlinks/special
// files are rejected, destination path components must not be symlinks, and the
// cumulative decompressed bytes copied for regular files must not exceed
// maxBytes. Symlinks are allowed only when their resolved target stays inside
// dest; later writes through symlink path components are still rejected. A
// maxBytes value <= 0 uses a conservative default cap.
func ExtractArchive(archivePath, dest string, maxBytes int64) ([]string, error) {
	if maxBytes <= 0 {
		maxBytes = defaultExtractMaxBytes
	}
	root, err := filepath.Abs(dest)
	if err != nil {
		return nil, fmt.Errorf("resolve extraction root: %w", err)
	}
	root = canonicalizeWellKnownRootSymlinks(root)
	if err := rejectExistingSymlinks(root); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, fmt.Errorf("create extraction root: %w", err)
	}
	if err := rejectExistingSymlinks(root); err != nil {
		return nil, err
	}

	lower := strings.ToLower(archivePath)
	budget := &extractBudget{max: maxBytes}
	switch {
	case strings.HasSuffix(lower, ".zip"):
		return extractZipArchive(archivePath, root, budget)
	case strings.HasSuffix(lower, ".tar.gz") || strings.HasSuffix(lower, ".tgz"):
		return extractTarGzArchive(archivePath, root, budget)
	default:
		return nil, fmt.Errorf("unsupported archive format %q", archivePath)
	}
}

func extractTarGzArchive(archivePath, root string, budget *extractBudget) ([]string, error) {
	f, err := os.Open(archivePath) //nolint:gosec // archivePath is supplied by provisioning after checksum-pinned download or tests.
	if err != nil {
		return nil, fmt.Errorf("open tar.gz archive: %w", err)
	}
	defer func() { _ = f.Close() }()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return nil, fmt.Errorf("read gzip header: %w", err)
	}
	defer func() { _ = gz.Close() }()

	tr := tar.NewReader(gz)
	var files []string
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read tar entry: %w", err)
		}
		rel, target, err := resolveArchiveTarget(root, hdr.Name)
		if err != nil {
			return nil, fmt.Errorf("tar entry %q: %w", hdr.Name, err)
		}

		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := makeExtractDir(root, target, modeOrDefault(hdr.FileInfo().Mode().Perm(), 0o755)); err != nil {
				return nil, fmt.Errorf("extract directory %q: %w", rel, err)
			}
		case tar.TypeReg:
			if err := writeExtractFile(root, target, hdr.FileInfo().Mode().Perm(), tr, budget); err != nil {
				return nil, fmt.Errorf("extract file %q: %w", rel, err)
			}
			files = append(files, rel)
		case tar.TypeSymlink:
			if err := createExtractSymlink(root, target, hdr.Linkname); err != nil {
				return nil, fmt.Errorf("extract symlink %q: %w", rel, err)
			}
			files = append(files, rel)
		case tar.TypeLink:
			return nil, fmt.Errorf("tar entry %q: hardlinks are not allowed", hdr.Name)
		default:
			return nil, fmt.Errorf("tar entry %q: unsupported file type %q", hdr.Name, string([]byte{hdr.Typeflag}))
		}
	}
	return files, nil
}

func extractZipArchive(archivePath, root string, budget *extractBudget) ([]string, error) {
	zr, err := zip.OpenReader(archivePath)
	if err != nil {
		return nil, fmt.Errorf("open zip archive: %w", err)
	}
	defer func() { _ = zr.Close() }()

	var files []string
	for _, zf := range zr.File {
		rel, target, err := resolveArchiveTarget(root, zf.Name)
		if err != nil {
			return nil, fmt.Errorf("zip entry %q: %w", zf.Name, err)
		}
		mode := zf.FileInfo().Mode()
		if mode&os.ModeSymlink != 0 {
			r, err := zf.Open()
			if err != nil {
				return nil, fmt.Errorf("open zip symlink %q: %w", zf.Name, err)
			}
			linkBytes, readErr := io.ReadAll(io.LimitReader(r, 4097))
			_ = r.Close()
			if readErr != nil {
				return nil, fmt.Errorf("read zip symlink %q: %w", zf.Name, readErr)
			}
			if len(linkBytes) > 4096 {
				return nil, fmt.Errorf("zip symlink %q target is too long", zf.Name)
			}
			if err := createExtractSymlink(root, target, string(linkBytes)); err != nil {
				return nil, fmt.Errorf("extract symlink %q: %w", rel, err)
			}
			files = append(files, rel)
			continue
		}
		if zf.FileInfo().IsDir() {
			if err := makeExtractDir(root, target, modeOrDefault(mode.Perm(), 0o755)); err != nil {
				return nil, fmt.Errorf("extract directory %q: %w", rel, err)
			}
			continue
		}
		if !mode.IsRegular() {
			return nil, fmt.Errorf("zip entry %q: unsupported file type %v", zf.Name, mode.Type())
		}
		r, err := zf.Open()
		if err != nil {
			return nil, fmt.Errorf("open zip entry %q: %w", zf.Name, err)
		}
		err = writeExtractFile(root, target, mode.Perm(), r, budget)
		_ = r.Close()
		if err != nil {
			return nil, fmt.Errorf("extract file %q: %w", rel, err)
		}
		files = append(files, rel)
	}
	return files, nil
}

func resolveArchiveTarget(root, name string) (string, string, error) {
	rel, err := cleanArchiveName(name)
	if err != nil {
		return "", "", err
	}
	target := filepath.Join(root, filepath.FromSlash(rel))
	if err := ensureUnderRoot(root, target); err != nil {
		return "", "", err
	}
	return rel, target, nil
}

func cleanArchiveName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", fmt.Errorf("empty archive path")
	}
	if strings.Contains(name, "\\") {
		return "", fmt.Errorf("backslash path separators are not allowed")
	}
	if hasWindowsDrivePrefix(name) || filepath.VolumeName(name) != "" {
		return "", fmt.Errorf("windows drive/volume paths are not allowed")
	}
	if strings.HasPrefix(name, "/") || slashpath.IsAbs(name) {
		return "", fmt.Errorf("absolute paths are not allowed")
	}
	name = strings.TrimRight(name, "/")
	if name == "" {
		return "", fmt.Errorf("empty archive path")
	}
	parts := strings.Split(name, "/")
	for _, part := range parts {
		if part == "" || part == "." {
			return "", fmt.Errorf("empty or dot path components are not allowed")
		}
		if part == ".." {
			return "", fmt.Errorf("parent directory traversal is not allowed")
		}
	}
	clean := slashpath.Clean(name)
	if clean == "." || clean == "" || strings.HasPrefix(clean, "../") || clean == ".." || slashpath.IsAbs(clean) {
		return "", fmt.Errorf("invalid archive path")
	}
	return clean, nil
}

func hasWindowsDrivePrefix(name string) bool {
	return len(name) >= 2 && name[1] == ':' && ((name[0] >= 'A' && name[0] <= 'Z') || (name[0] >= 'a' && name[0] <= 'z'))
}

func ensureUnderRoot(root, target string) error {
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return fmt.Errorf("resolve relative target: %w", err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return fmt.Errorf("target escapes extraction root")
	}
	return nil
}

func makeExtractDir(root, target string, mode os.FileMode) error {
	parent := filepath.Dir(target)
	if err := rejectSymlinkPath(root, parent); err != nil {
		return err
	}
	if err := os.MkdirAll(target, mode); err != nil {
		return err
	}
	if err := rejectSymlinkPath(root, target); err != nil {
		return err
	}
	return os.Chmod(target, mode)
}

func createExtractSymlink(root, target, linkname string) error {
	if strings.TrimSpace(linkname) == "" {
		return fmt.Errorf("empty symlink target")
	}
	if strings.Contains(linkname, "\\") || hasWindowsDrivePrefix(linkname) || filepath.VolumeName(linkname) != "" {
		return fmt.Errorf("invalid symlink target %q", linkname)
	}
	if slashpath.IsAbs(linkname) || filepath.IsAbs(linkname) {
		return fmt.Errorf("absolute symlink target %q is not allowed", linkname)
	}
	resolved := filepath.Clean(filepath.Join(filepath.Dir(target), filepath.FromSlash(linkname)))
	if !pathWithin(root, resolved) {
		return fmt.Errorf("symlink target %q escapes extraction root", linkname)
	}
	parent := filepath.Dir(target)
	if err := rejectSymlinkPath(root, parent); err != nil {
		return err
	}
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return err
	}
	if err := rejectSymlinkPath(root, parent); err != nil {
		return err
	}
	if err := rejectSymlinkPath(root, target); err != nil {
		return err
	}
	if _, err := os.Lstat(target); err == nil {
		return fmt.Errorf("destination already exists")
	} else if err != nil && !os.IsNotExist(err) {
		return err
	}
	return os.Symlink(linkname, target)
}

func writeExtractFile(root, target string, mode os.FileMode, src io.Reader, budget *extractBudget) error {
	parent := filepath.Dir(target)
	if err := rejectSymlinkPath(root, parent); err != nil {
		return err
	}
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return err
	}
	if err := rejectSymlinkPath(root, parent); err != nil {
		return err
	}
	if err := rejectSymlinkPath(root, target); err != nil {
		return err
	}

	fileMode := modeOrDefault(mode.Perm(), 0o644)
	f, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, fileMode) //nolint:gosec // target is validated to stay under the extraction root before opening.
	if os.IsExist(err) {
		return fmt.Errorf("destination already exists")
	}
	if err != nil {
		return err
	}
	cleanup := true
	defer func() {
		_ = f.Close()
		if cleanup {
			_ = os.Remove(target)
		}
	}()
	if _, err := io.Copy(f, budget.reader(src)); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Chmod(target, fileMode); err != nil {
		return err
	}
	cleanup = false
	return nil
}

func canonicalizeWellKnownRootSymlinks(p string) string {
	clean := filepath.Clean(p)
	for _, prefix := range []string{"/var", "/tmp", "/etc"} {
		if clean != prefix && !strings.HasPrefix(clean, prefix+string(filepath.Separator)) {
			continue
		}
		target, err := os.Readlink(prefix)
		if err != nil {
			continue
		}
		if !filepath.IsAbs(target) {
			target = filepath.Join(filepath.Dir(prefix), target)
		}
		return filepath.Clean(target) + strings.TrimPrefix(clean, prefix)
	}
	return clean
}

func rejectExistingSymlinks(path string) error {
	clean := filepath.Clean(path)
	volume := filepath.VolumeName(clean)
	rest := strings.TrimPrefix(clean, volume)
	cur := volume
	if filepath.IsAbs(clean) {
		cur = volume + string(filepath.Separator)
		rest = strings.TrimPrefix(rest, string(filepath.Separator))
	}
	for _, part := range strings.Split(rest, string(filepath.Separator)) {
		if part == "" || part == "." {
			continue
		}
		if cur == "" || cur == string(filepath.Separator) || strings.HasSuffix(cur, string(filepath.Separator)) {
			cur = cur + part
		} else {
			cur = filepath.Join(cur, part)
		}
		info, err := os.Lstat(cur)
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("path component %q is a symlink", cur)
		}
	}
	return nil
}

func rejectSymlinkPath(root, target string) error {
	if err := ensureUnderRoot(root, target); err != nil {
		return err
	}
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return err
	}
	if rel == "." {
		info, err := os.Lstat(root)
		if err == nil && info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("extraction root is a symlink")
		}
		return nil
	}
	cur := root
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		if part == "." || part == "" {
			continue
		}
		cur = filepath.Join(cur, part)
		info, err := os.Lstat(cur)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("path component %q is a symlink", cur)
		}
	}
	return nil
}

func modeOrDefault(mode, fallback os.FileMode) os.FileMode {
	mode = mode.Perm()
	if mode == 0 {
		return fallback
	}
	return mode
}

type extractBudget struct {
	max  int64
	used int64
}

func (b *extractBudget) reader(r io.Reader) io.Reader {
	return &budgetReader{r: r, budget: b}
}

type budgetReader struct {
	r      io.Reader
	budget *extractBudget
}

func (r *budgetReader) Read(p []byte) (int, error) {
	if r.budget.max >= 0 {
		remaining := r.budget.max - r.budget.used
		if remaining <= 0 {
			var probe [1]byte
			n, err := r.r.Read(probe[:])
			if n > 0 {
				return 0, fmt.Errorf("extracted data exceeds limit of %d bytes", r.budget.max)
			}
			return 0, err
		}
		if int64(len(p)) > remaining {
			p = p[:remaining]
		}
	}
	n, err := r.r.Read(p)
	r.budget.used += int64(n)
	return n, err
}
