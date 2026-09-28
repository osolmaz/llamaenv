package runtimes

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/osolmaz/llamaenv/internal/config"
)

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func newConfig(t *testing.T) *config.Config {
	t.Helper()
	c, err := config.Load(config.Dirs{Config: t.TempDir(), Data: t.TempDir()})
	must(t, err)
	return c
}

func writeProgram(t *testing.T, path string) {
	t.Helper()
	must(t, os.MkdirAll(filepath.Dir(path), 0o750))
	must(t, os.WriteFile(path, []byte("x"), 0o600))
}

func zipOf(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var b bytes.Buffer
	w := zip.NewWriter(&b)
	for name, body := range files {
		f, err := w.Create(name)
		must(t, err)
		_, err = f.Write([]byte(body))
		must(t, err)
	}
	must(t, w.Close())
	return b.Bytes()
}

// entry is one tar entry: a file with a body, a folder (name ends in "/"),
// or a symlink (link is set).
type entry struct{ name, body, link string }

func tgzOf(t *testing.T, entries ...entry) []byte {
	t.Helper()
	var b bytes.Buffer
	gz := gzip.NewWriter(&b)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		h := &tar.Header{Name: e.name, Mode: 0o755, Size: int64(len(e.body)), Typeflag: tar.TypeReg}
		switch {
		case e.link != "":
			h.Typeflag, h.Linkname, h.Size = tar.TypeSymlink, e.link, 0
		case strings.HasSuffix(e.name, "/"):
			h.Typeflag = tar.TypeDir
		}
		must(t, tw.WriteHeader(h))
		if h.Typeflag == tar.TypeReg {
			_, err := tw.Write([]byte(e.body))
			must(t, err)
		}
	}
	must(t, tw.Close())
	must(t, gz.Close())
	return b.Bytes()
}

// serveFiles serves archives by path. The map may change during a test.
func serveFiles(t *testing.T, files map[string][]byte) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, ok := files[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// prismLike is a Prism Windows install: the build, and the CUDA runtime DLLs
// that must sit next to llama-server.exe.
func prismLike(t *testing.T) (*config.Config, map[string][]byte, []string) {
	t.Helper()
	files := map[string][]byte{
		"/llama-prism-bin.tar.gz": tgzOf(t, entry{name: "llama-prism-b1/" + Exe("llama-server"), body: "server"}),
		"/cudart.zip":             zipOf(t, map[string]string{"cudart64_13.dll": "dll"}),
	}
	srv := serveFiles(t, files)
	return newConfig(t), files, []string{srv.URL + "/llama-prism-bin.tar.gz", srv.URL + "/cudart.zip"}
}

func TestAddUnpacksArchivesIntoOneFolder(t *testing.T) {
	c, _, urls := prismLike(t)
	rt, err := Add(c, "prism", urls, func(string) {})
	must(t, err)
	if filepath.Base(rt.Server) != Exe("llama-server") || rt.Llama != "" || rt.CanBeDefault() {
		t.Errorf("runtime %+v", rt)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(rt.Server), "cudart64_13.dll")); err != nil {
		t.Error("cudart DLL is not next to llama-server:", err)
	}
	if src, _ := c.Runtimes.Get("runtime prism", Platform()); src != strings.Join(urls, " ") {
		t.Errorf("source %q", src)
	}
}

func TestAddPinsHashesAndRefusesAChangedArchive(t *testing.T) {
	c, files, urls := prismLike(t)
	_, err := Add(c, "prism", urls, func(string) {})
	must(t, err)
	if sum, ok := c.Lock.Get(urls[0], "sha256"); !ok || len(sum) != 64 {
		t.Errorf("no pinned hash: %q", sum)
	}
	files["/cudart.zip"] = zipOf(t, map[string]string{"cudart64_13.dll": "tampered"})
	if _, err := Add(c, "prism", urls, func(string) {}); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Errorf("tampered archive: %v", err)
	}
	if _, err := Resolve(c, "prism"); err != nil {
		t.Errorf("runtime lost after a refused update: %v", err)
	}
}

func TestAddUsesAFolderInPlace(t *testing.T) {
	dir := t.TempDir()
	server := filepath.Join(dir, "build", "bin", Exe("llama-server"))
	writeProgram(t, server)
	c := newConfig(t)
	rt, err := Add(c, "prism", []string{dir}, func(string) {})
	must(t, err)
	if rt.Server != server {
		t.Errorf("server %s", rt.Server)
	}
	must(t, Remove(c, "prism"))
	if _, err := os.Stat(server); err != nil {
		t.Error("removing a runtime deleted a folder that was used in place")
	}
}

func TestUnifiedLlamaCanBeTheDefault(t *testing.T) {
	dir := t.TempDir()
	writeProgram(t, filepath.Join(dir, Exe("llama")))
	c := newConfig(t)
	rt, err := Add(c, "official-b1", []string{dir}, func(string) {})
	must(t, err)
	prog, prefix := rt.Launch()
	if !rt.CanBeDefault() || filepath.Base(prog) != Exe("llama") || len(prefix) != 1 || prefix[0] != "serve" {
		t.Errorf("%+v %s %v", rt, prog, prefix)
	}
}

func TestArchivePathsCannotEscape(t *testing.T) {
	if _, err := safeJoin(t.TempDir(), "../evil"); err == nil {
		t.Error("escape accepted")
	}
}

func TestInvalidNames(t *testing.T) {
	c := newConfig(t)
	for _, n := range []string{"", "official", "a/b", "a:b"} {
		if _, err := Add(c, n, []string{t.TempDir()}, func(string) {}); err == nil {
			t.Errorf("%q accepted", n)
		}
	}
}

func TestInstallRestoresARemovedDownload(t *testing.T) {
	files := map[string][]byte{"/b.tar.gz": tgzOf(t, entry{name: "b/lib/"}, entry{name: "b/" + Exe("llama"), body: "x"})}
	srv := serveFiles(t, files)
	c := newConfig(t)
	_, err := Add(c, "b", []string{srv.URL + "/b.tar.gz"}, func(string) {})
	must(t, err)
	if n := Names(c); len(n) != 1 || n[0] != "b" {
		t.Errorf("names %v", n)
	}
	must(t, os.RemoveAll(filepath.Join(c.Dirs.Runtimes(), "b")))
	rt, err := Install(c, "b", func(string) {})
	if err != nil || rt.Llama == "" {
		t.Fatalf("install: %+v %v", rt, err)
	}
	delete(files, "/b.tar.gz") // already installed: no download needed
	if _, err := Install(c, "b", func(string) {}); err != nil {
		t.Error(err)
	}
}

func TestRemoveDeletesADownloadedRuntime(t *testing.T) {
	srv := serveFiles(t, map[string][]byte{"/b.tar.gz": tgzOf(t, entry{name: Exe("llama"), body: "x"})})
	c := newConfig(t)
	_, err := Add(c, "b", []string{srv.URL + "/b.tar.gz"}, func(string) {})
	must(t, err)
	must(t, Remove(c, "b"))
	if _, err := os.Stat(filepath.Join(c.Dirs.Runtimes(), "b")); !os.IsNotExist(err) {
		t.Error("downloaded runtime not deleted")
	}
	if _, err := Install(c, "b", func(string) {}); err == nil {
		t.Error("installed an unknown runtime")
	}
}

func TestDownloadErrors(t *testing.T) {
	srv := serveFiles(t, map[string][]byte{"/x.rar": []byte("x"), "/x.zip": []byte("x")})
	c := newConfig(t)
	cases := map[string]string{
		srv.URL + "/missing.zip": "404",
		srv.URL + "/x.rar":       "unsupported",
		srv.URL + "/x.zip":       "zip",
	}
	for url, want := range cases {
		if _, err := Add(c, "x", []string{url}, func(string) {}); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: got %v, want %q", url, err, want)
		}
	}
}

func TestAddNeedsAUsableSource(t *testing.T) {
	c := newConfig(t)
	if _, err := Add(c, "x", nil, func(string) {}); err == nil {
		t.Error("no sources accepted")
	}
	if _, err := Add(c, "x", []string{t.TempDir()}, func(string) {}); err == nil || !strings.Contains(err.Error(), "no ") {
		t.Errorf("empty folder: %v", err)
	}
	if _, err := Resolve(c, "x"); err == nil {
		t.Error("resolved a runtime that was never added")
	}
}

func TestUntarKeepsSymlinks(t *testing.T) {
	data := tgzOf(t, entry{name: "r/"}, entry{name: "r/libx.so.1", body: "x"}, entry{name: "r/libx.so", link: "libx.so.1"})
	archive := filepath.Join(t.TempDir(), "r.tar.gz")
	must(t, os.WriteFile(archive, data, 0o600))
	dest := t.TempDir()
	must(t, unpackFlat(archive, dest))
	if target, err := os.Readlink(filepath.Join(dest, "libx.so")); err != nil || target != "libx.so.1" {
		t.Errorf("symlink: %q %v", target, err)
	}
}

func TestCopyLimitedRefusesOversizedFiles(t *testing.T) {
	var b bytes.Buffer
	if _, err := copyLimited(&b, strings.NewReader("12345"), 4); err == nil {
		t.Error("oversized file accepted")
	}
	if n, err := copyLimited(&b, strings.NewReader("1234"), 4); err != nil || n != 4 {
		t.Errorf("file at the limit: %d %v", n, err)
	}
}

func TestHashesArePinnedForURLsWithQueries(t *testing.T) {
	files := map[string][]byte{"/b.tar.gz": tgzOf(t, entry{name: Exe("llama"), body: "x"})}
	srv := serveFiles(t, files)
	c := newConfig(t)
	url := srv.URL + "/b.tar.gz?sig=abc&x=1"
	_, err := Add(c, "b", []string{url}, func(string) {})
	must(t, err)
	must(t, c.Save())
	reloaded, err := config.Load(c.Dirs)
	must(t, err)
	files["/b.tar.gz"] = tgzOf(t, entry{name: Exe("llama"), body: "tampered"})
	if _, err := Add(reloaded, "b", []string{url}, func(string) {}); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Errorf("tampered archive behind a URL with a query: %v", err)
	}
}

func TestUntarRefusesLinksThatLeaveTheFolder(t *testing.T) {
	for _, target := range []string{"../../evil", "/etc/passwd"} {
		data := tgzOf(t, entry{name: "r/x", link: target}, entry{name: "r/x/file", body: "x"})
		archive := filepath.Join(t.TempDir(), "r.tar.gz")
		must(t, os.WriteFile(archive, data, 0o600))
		if err := unpackFlat(archive, t.TempDir()); err == nil {
			t.Errorf("link to %q accepted", target)
		}
	}
}

func TestPresetFromAURLIsPinned(t *testing.T) {
	preset := "[prism-ml/Ternary-Bonsai-2-27B-gguf:PQ2_0]\nctx-size = 98304\n"
	files := map[string][]byte{"/preset.ini": []byte(preset), "/big.ini": make([]byte, maxPresetSize+1)}
	srv := serveFiles(t, files)
	c := newConfig(t)
	dir := t.TempDir()
	writeProgram(t, filepath.Join(dir, Exe("llama-server")))
	_, err := Add(c, "prism", []string{dir}, func(string) {})
	must(t, err)
	url := srv.URL + "/preset.ini"
	dst, err := AddPreset(c, "prism", url, func(string) {})
	must(t, err)
	if data, _ := os.ReadFile(dst); string(data) != preset { //nolint:gosec // the test's own file
		t.Errorf("saved %q", data)
	}
	if sum, ok := c.Lock.Get(url, "sha256"); !ok || len(sum) != 64 {
		t.Errorf("not pinned: %q", sum)
	}
	files["/preset.ini"] = []byte(preset + "parallel = 4\n")
	if _, err := AddPreset(c, "prism", url, func(string) {}); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Errorf("changed preset accepted: %v", err)
	}
	if _, err := AddPreset(c, "prism", srv.URL+"/big.ini", func(string) {}); err == nil || !strings.Contains(err.Error(), "larger than") {
		t.Errorf("big preset accepted: %v", err)
	}
	if _, err := AddPreset(c, "prism", srv.URL+"/missing.ini", func(string) {}); err == nil {
		t.Error("missing preset accepted")
	}
	if err := RemovePreset(c, "prism", "other.ini"); err == nil {
		t.Error("removed a preset that was never added")
	}
}

func TestPresetNameIsTheFileName(t *testing.T) {
	for _, bad := range []string{"https://x/y/", "/p/no-suffix", "/p/with space.ini", "/p/.ini"} {
		if _, err := presetName(bad); err == nil {
			t.Errorf("%q accepted as a preset name", bad)
		}
	}
	if n, _ := presetName("https://x/bonsai-2-27b.ini?download=1"); n != "bonsai-2-27b.ini" {
		t.Errorf("name from URL %q", n)
	}
}
