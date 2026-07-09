package provision

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type tarEntry struct {
	name     string
	body     []byte
	mode     int64
	typeflag byte
	linkname string
}

func writeTarGzFixture(t *testing.T, entries ...tarEntry) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fixture.tar.gz")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600) //nolint:gosec // path is a fixture archive path inside t.TempDir.
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		mode := e.mode
		if mode == 0 {
			mode = 0o644
		}
		typeflag := e.typeflag
		if typeflag == 0 {
			typeflag = tar.TypeReg
		}
		hdr := &tar.Header{Name: e.name, Mode: mode, Typeflag: typeflag, Linkname: e.linkname}
		if typeflag == tar.TypeReg {
			hdr.Size = int64(len(e.body))
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if hdr.Size > 0 {
			if _, err := tw.Write(e.body); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

type zipEntry struct {
	name string
	body []byte
	mode os.FileMode
}

func writeZipFixture(t *testing.T, entries ...zipEntry) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fixture.zip")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600) //nolint:gosec // path is a fixture archive path inside t.TempDir.
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	for _, e := range entries {
		hdr := &zip.FileHeader{Name: e.name}
		mode := e.mode
		if mode == 0 {
			mode = 0o644
		}
		hdr.SetMode(mode)
		w, err := zw.CreateHeader(hdr)
		if err != nil {
			t.Fatal(err)
		}
		if len(e.body) > 0 {
			if _, err := w.Write(e.body); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestExtractTarGzHappyPathPreservesExecutableBit(t *testing.T) {
	archive := writeTarGzFixture(t,
		tarEntry{name: "bundle/", typeflag: tar.TypeDir, mode: 0o755},
		tarEntry{name: "bundle/llama-server", body: []byte("engine"), mode: 0o755},
		tarEntry{name: "bundle/README.txt", body: []byte("notes"), mode: 0o644},
	)
	dest := t.TempDir()
	files, err := ExtractArchive(archive, dest, 1024)
	if err != nil {
		t.Fatalf("ExtractArchive: %v", err)
	}
	if got := strings.Join(files, ","); got != "bundle/llama-server,bundle/README.txt" {
		t.Fatalf("files = %q", got)
	}
	bin := filepath.Join(dest, "bundle", "llama-server")
	if got, err := os.ReadFile(bin); err != nil || string(got) != "engine" { //nolint:gosec // bin is a validated extraction output path inside t.TempDir.
		t.Fatalf("binary content mismatch: %q %v", got, err)
	}
	info, err := os.Stat(bin)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Fatalf("mode = %v, want 0755", info.Mode().Perm())
	}
}

func TestExtractZipHappyPath(t *testing.T) {
	archive := writeZipFixture(t,
		zipEntry{name: "bundle/", mode: os.ModeDir | 0o755},
		zipEntry{name: "bundle/llama-server", body: []byte("engine"), mode: 0o755},
	)
	dest := t.TempDir()
	files, err := ExtractArchive(archive, dest, 1024)
	if err != nil {
		t.Fatalf("ExtractArchive: %v", err)
	}
	if len(files) != 1 || files[0] != "bundle/llama-server" {
		t.Fatalf("files = %+v", files)
	}
	if got, err := os.ReadFile(filepath.Join(dest, "bundle", "llama-server")); err != nil || string(got) != "engine" { //nolint:gosec // path is a validated extraction output inside t.TempDir.
		t.Fatalf("extracted content mismatch: %q %v", got, err)
	}
}

func TestExtractRejectsTraversalAndAbsolutePaths(t *testing.T) {
	for _, tc := range []struct {
		name    string
		archive string
	}{
		{name: "tar traversal", archive: writeTarGzFixture(t, tarEntry{name: "../pwned", body: []byte("x")})},
		{name: "tar absolute", archive: writeTarGzFixture(t, tarEntry{name: "/tmp/pwned", body: []byte("x")})},
		{name: "zip traversal", archive: writeZipFixture(t, zipEntry{name: "dir/../../pwned", body: []byte("x")})},
		{name: "zip absolute", archive: writeZipFixture(t, zipEntry{name: "/tmp/pwned", body: []byte("x")})},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ExtractArchive(tc.archive, t.TempDir(), 1024); err == nil {
				t.Fatal("expected extraction error")
			}
		})
	}
}

func TestExtractAllowsSafeInBundleSymlinks(t *testing.T) {
	archive := writeTarGzFixture(t,
		tarEntry{name: "bundle/libreal.dylib", body: []byte("lib"), mode: 0o644},
		tarEntry{name: "bundle/libalias.dylib", typeflag: tar.TypeSymlink, linkname: "libreal.dylib"},
	)
	dest := t.TempDir()
	files, err := ExtractArchive(archive, dest, 1024)
	if err != nil {
		t.Fatalf("ExtractArchive: %v", err)
	}
	if strings.Join(files, ",") != "bundle/libreal.dylib,bundle/libalias.dylib" {
		t.Fatalf("files = %+v", files)
	}
	link := filepath.Join(dest, "bundle", "libalias.dylib")
	target, err := os.Readlink(link)
	if err != nil {
		t.Fatalf("expected symlink: %v", err)
	}
	if target != "libreal.dylib" {
		t.Fatalf("symlink target = %q", target)
	}
}

func TestExtractRejectsSymlinksHardlinksAndSpecialFiles(t *testing.T) {
	for _, tc := range []struct {
		name    string
		archive string
		want    string
	}{
		{name: "tar symlink", archive: writeTarGzFixture(t, tarEntry{name: "link", typeflag: tar.TypeSymlink, linkname: "/tmp/outside"}), want: "absolute symlink"},
		{name: "tar hardlink", archive: writeTarGzFixture(t, tarEntry{name: "hard", typeflag: tar.TypeLink, linkname: "../../outside"}), want: "hardlinks"},
		{name: "tar fifo", archive: writeTarGzFixture(t, tarEntry{name: "fifo", typeflag: tar.TypeFifo}), want: "unsupported"},
		{name: "zip symlink", archive: writeZipFixture(t, zipEntry{name: "link", body: []byte("/tmp/outside"), mode: os.ModeSymlink | 0o777}), want: "absolute symlink"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ExtractArchive(tc.archive, t.TempDir(), 1024)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want substring %q", err, tc.want)
			}
		})
	}
}

func TestExtractRejectsSymlinkAncestorOfExtractionRoot(t *testing.T) {
	outside := t.TempDir()
	base := t.TempDir()
	link := filepath.Join(base, "tmp-link")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	archive := writeZipFixture(t, zipEntry{name: "llama-server", body: []byte("engine")})
	_, err := ExtractArchive(archive, filepath.Join(link, "extract"), 1024)
	if err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("expected symlink ancestor rejection, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(outside, "extract", "llama-server")); !os.IsNotExist(err) {
		t.Fatalf("outside-root file should not exist: %v", err)
	}
}

func TestExtractRejectsPreexistingSymlinkPathComponent(t *testing.T) {
	outside := t.TempDir()
	dest := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(dest, "dir")); err != nil {
		t.Fatal(err)
	}
	archive := writeTarGzFixture(t, tarEntry{name: "dir/pwned", body: []byte("owned")})
	if _, err := ExtractArchive(archive, dest, 1024); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("expected symlink path-component rejection, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(outside, "pwned")); !os.IsNotExist(err) {
		t.Fatalf("outside-root file should not exist: %v", err)
	}
}

func TestExtractArchiveSymlinkEntryDoesNotWriteOutsideRoot(t *testing.T) {
	outside := t.TempDir()
	dest := t.TempDir()
	archive := writeTarGzFixture(t,
		tarEntry{name: "dir", typeflag: tar.TypeSymlink, linkname: outside},
		tarEntry{name: "dir/pwned", body: []byte("owned")},
	)
	if _, err := ExtractArchive(archive, dest, 1024); err == nil || !strings.Contains(err.Error(), "absolute symlink") {
		t.Fatalf("expected symlink rejection, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(outside, "pwned")); !os.IsNotExist(err) {
		t.Fatalf("outside-root file should not exist: %v", err)
	}
}

func TestExtractRejectsDriveAndBackslashZipPaths(t *testing.T) {
	for _, name := range []string{`C:/temp/pwned`, `dir\\pwned`} {
		t.Run(name, func(t *testing.T) {
			archive := writeZipFixture(t, zipEntry{name: name, body: []byte("x")})
			if _, err := ExtractArchive(archive, t.TempDir(), 1024); err == nil {
				t.Fatal("expected invalid path error")
			}
		})
	}
}

func TestExtractCumulativeSizeCapAbortsAndCleansPartialFile(t *testing.T) {
	archive := writeZipFixture(t,
		zipEntry{name: "one", body: bytes.Repeat([]byte("a"), 5)},
		zipEntry{name: "two", body: bytes.Repeat([]byte("b"), 5)},
	)
	dest := t.TempDir()
	_, err := ExtractArchive(archive, dest, 8)
	if err == nil || !strings.Contains(err.Error(), "exceeds limit") {
		t.Fatalf("expected size cap error, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, "one")); err != nil {
		t.Fatalf("first file should remain after cumulative cap failure: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, "two")); !os.IsNotExist(err) {
		t.Fatalf("partial second file should be removed: %v", err)
	}
}

func TestExtractRejectsEmptyDotAndDuplicateDestination(t *testing.T) {
	archive := writeTarGzFixture(t, tarEntry{name: "./pwned", body: []byte("x")})
	if _, err := ExtractArchive(archive, t.TempDir(), 1024); err == nil {
		t.Fatal("expected dot component rejection")
	}

	dest := t.TempDir()
	if err := os.WriteFile(filepath.Join(dest, "exists"), []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	archive = writeZipFixture(t, zipEntry{name: "exists", body: []byte("new")})
	if _, err := ExtractArchive(archive, dest, 1024); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("expected existing destination rejection, got %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dest, "exists")) //nolint:gosec // path is a fixture file inside t.TempDir.
	if err != nil || string(got) != "old" {
		t.Fatalf("existing file was modified: %q %v", got, err)
	}
}

func TestBudgetReaderErrorsOnlyWhenStreamExceedsLimit(t *testing.T) {
	budget := &extractBudget{max: 3}
	got, err := io.ReadAll(budget.reader(strings.NewReader("abc")))
	if err != nil || string(got) != "abc" {
		t.Fatalf("exact-limit read = %q, %v", got, err)
	}
	_, err = io.ReadAll(budget.reader(strings.NewReader("d")))
	if err == nil || !strings.Contains(err.Error(), "exceeds limit") {
		t.Fatalf("expected over-limit error, got %v", err)
	}
}
