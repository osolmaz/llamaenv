package runtimes

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/osolmaz/llamaenv/internal/config"
)

// maxFileSize bounds each unpacked file, so a broken or hostile archive
// cannot fill the disk. The largest llama.cpp CUDA libraries are under 2 GB.
const maxFileSize = 8 << 30

// installArchives downloads each archive, checks it against runtimes.lock,
// and unpacks all of them into one folder. The first download of a URL
// records its SHA-256; later downloads must match it. The previous install
// stays in place until the new one is complete.
func installArchives(c *config.Config, dest string, urls []string, log func(string)) error {
	stage := dest + ".part"
	if err := os.RemoveAll(stage); err != nil {
		return err
	}
	if err := os.MkdirAll(stage, 0o750); err != nil {
		return err
	}
	for _, u := range urls {
		if err := installArchive(c, stage, u, log); err != nil {
			return err
		}
	}
	if err := os.RemoveAll(dest); err != nil {
		return err
	}
	return os.Rename(stage, dest)
}

func installArchive(c *config.Config, stage, url string, log func(string)) error {
	file, sum, err := download(c.Dirs.Data, url, log)
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(file) }()
	// Each URL is a section: a URL may contain "=", which a key cannot.
	if want, ok := c.Lock.Get(url, "sha256"); ok && !strings.EqualFold(want, sum) {
		return fmt.Errorf("%s: SHA-256 %s does not match the pinned %s in runtimes.lock", url, sum, want)
	}
	c.Lock.Set(url, "sha256", sum)
	if err := unpackFlat(file, stage); err != nil {
		return fmt.Errorf("unpack %s: %w", url, err)
	}
	return nil
}

func download(dataDir, url string, log func(string)) (string, string, error) {
	dir := filepath.Join(dataDir, "downloads")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", "", err
	}
	file := filepath.Join(dir, path.Base(strings.SplitN(url, "?", 2)[0]))
	log("downloading " + url)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("%s: HTTP %s", url, resp.Status)
	}
	h := sha256.New()
	n, err := writeFrom(file, 0o600, io.TeeReader(resp.Body, h))
	if err != nil {
		return "", "", err
	}
	log(fmt.Sprintf("downloaded %.1f MB", float64(n)/(1<<20)))
	return file, hex.EncodeToString(h.Sum(nil)), nil
}

// unpackFlat unpacks a .zip or .tar.gz into dest. When the archive holds one
// top-level folder, its contents go straight into dest.
func unpackFlat(archive, dest string) error {
	tmp, err := os.MkdirTemp(filepath.Dir(dest), "unpack-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	if err := unpack(archive, tmp); err != nil {
		return err
	}
	return moveContents(singleFolder(tmp), dest)
}

func unpack(archive, dest string) error {
	switch {
	case strings.HasSuffix(archive, ".zip"):
		return unzip(archive, dest)
	case strings.HasSuffix(archive, ".tar.gz"), strings.HasSuffix(archive, ".tgz"):
		return untar(archive, dest)
	}
	return fmt.Errorf("unsupported archive type: %s", filepath.Base(archive))
}

// singleFolder returns dir's only entry when that is a folder, else dir.
func singleFolder(dir string) string {
	entries, err := os.ReadDir(dir)
	if err == nil && len(entries) == 1 && entries[0].IsDir() {
		return filepath.Join(dir, entries[0].Name())
	}
	return dir
}

func moveContents(from, to string) error {
	entries, err := os.ReadDir(from)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if err := os.Rename(filepath.Join(from, e.Name()), filepath.Join(to, e.Name())); err != nil {
			return err
		}
	}
	return nil
}

// safeJoin rejects archive paths that would escape dest.
func safeJoin(dest, name string) (string, error) {
	p := filepath.Join(dest, filepath.FromSlash(name))
	if p != dest && !strings.HasPrefix(p, dest+string(filepath.Separator)) {
		return "", fmt.Errorf("archive path escapes the folder: %q", name)
	}
	return p, nil
}

func unzip(archive, dest string) error {
	r, err := zip.OpenReader(archive)
	if err != nil {
		return err
	}
	defer func() { _ = r.Close() }()
	for _, f := range r.File {
		if err := unzipEntry(f, dest); err != nil {
			return err
		}
	}
	return nil
}

func unzipEntry(f *zip.File, dest string) error {
	p, err := safeJoin(dest, f.Name)
	if err != nil {
		return err
	}
	if f.FileInfo().IsDir() {
		return os.MkdirAll(p, 0o750)
	}
	src, err := f.Open()
	if err != nil {
		return err
	}
	defer func() { _ = src.Close() }()
	_, err = writeFrom(p, programMode(f.Mode()&0o111 != 0 || isProgramName(f.Name)), src)
	return err
}

func untar(archive, dest string) error {
	f, err := os.Open(filepath.Clean(archive))
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		if err := untarEntry(h, tr, dest); err != nil {
			return err
		}
	}
}

func untarEntry(h *tar.Header, r io.Reader, dest string) error {
	p, err := safeJoin(dest, h.Name)
	if err != nil {
		return err
	}
	switch h.Typeflag {
	case tar.TypeDir:
		return os.MkdirAll(p, 0o750)
	case tar.TypeSymlink:
		if err := checkLink(dest, p, h.Linkname); err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			return err
		}
		_ = os.Remove(p) // replace an older link
		return os.Symlink(h.Linkname, p)
	case tar.TypeReg:
		_, err := writeFrom(p, programMode(h.Mode&0o111 != 0), r)
		return err
	}
	return nil // other entry types do not occur in llama.cpp builds
}

// checkLink refuses a symlink whose target leaves dest, so later entries
// cannot be written through it to other places.
func checkLink(dest, link, target string) error {
	if filepath.IsAbs(target) {
		return fmt.Errorf("archive link %q points to an absolute path", target)
	}
	resolved := filepath.Join(filepath.Dir(link), target)
	if resolved != dest && !strings.HasPrefix(resolved, dest+string(filepath.Separator)) {
		return fmt.Errorf("archive link %q points outside the folder", target)
	}
	return nil
}

// isProgramName marks Windows programs, since zip files made on Windows
// carry no executable bit.
func isProgramName(name string) bool { return strings.HasSuffix(strings.ToLower(name), ".exe") }

func programMode(program bool) os.FileMode {
	if program {
		return 0o700
	}
	return 0o600
}

// writeFrom writes r to a new file at p, at most maxFileSize bytes.
func writeFrom(p string, mode os.FileMode, r io.Reader) (int64, error) {
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		return 0, err
	}
	out, err := os.OpenFile(filepath.Clean(p), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return 0, err
	}
	n, err := copyLimited(out, r, maxFileSize)
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil || mode == 0o600 {
		return n, err
	}
	return n, os.Chmod(p, mode) // runtime programs must be executable by their owner
}

// copyLimited copies r to w and fails when r holds more than limit bytes.
func copyLimited(w io.Writer, r io.Reader, limit int64) (int64, error) {
	n, err := io.CopyN(w, r, limit+1)
	if errors.Is(err, io.EOF) {
		err = nil
	}
	if err == nil && n > limit {
		err = fmt.Errorf("file is larger than %d bytes", limit)
	}
	return n, err
}
