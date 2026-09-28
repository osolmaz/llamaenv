package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestINIKeepsCommentsWhenAValueChanges(t *testing.T) {
	src := "; my notes\ndefault = official\n\n[prism-ml/Ternary-Bonsai-2-27B-gguf]\n# until upstream\nruntime = prism\n"
	f, err := ParseINI(src)
	if err != nil {
		t.Fatal(err)
	}
	f.Set("prism-ml/Ternary-Bonsai-2-27B-gguf", "runtime", "prism-b2")
	f.Set("other/repo", "runtime", "x")
	got := f.String()
	for _, want := range []string{"; my notes", "# until upstream", "runtime = prism-b2", "[other/repo]\nruntime = x"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if f2, _ := ParseINI(got); f2.String() != got {
		t.Error("render is not stable")
	}
	if _, err := ParseINI("no equals sign"); err == nil {
		t.Error("bad line accepted")
	}
}

func TestNewFileHasNoLeadingBlankLines(t *testing.T) {
	f, _ := ParseINI("")
	f.Set("a/b", "runtime", "x")
	f.Set("c/d", "runtime", "y")
	if got, want := f.String(), "[a/b]\nruntime = x\n\n[c/d]\nruntime = y\n"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestRuntimeForPrefersTheExactModelID(t *testing.T) {
	c := &Config{}
	c.Models, _ = ParseINI("[prism-ml/Ternary-Bonsai-2-27B-gguf]\nruntime = prism\n\n[prism-ml/Ternary-Bonsai-2-27B-gguf:PTQ1_0]\nruntime = official\n")
	cases := map[string]string{
		"prism-ml/Ternary-Bonsai-2-27B-gguf:PQ2_0":  "prism",
		"prism-ml/ternary-bonsai-2-27b-gguf:PQ2_0":  "prism", // Hub repo IDs ignore case
		"prism-ml/Ternary-Bonsai-2-27B-gguf:PTQ1_0": "official",
		"ggml-org/gemma-4-e4b-it-GGUF:Q4_0":         "",
		"":                                          "",
	}
	for id, want := range cases {
		if got, _ := c.RuntimeFor(id); got != want {
			t.Errorf("%q: got %q, want %q", id, got, want)
		}
	}
}

func TestLoadAndSaveRoundTrip(t *testing.T) {
	d := Dirs{Config: t.TempDir(), Data: t.TempDir()}
	c, err := Load(d)
	if err != nil {
		t.Fatal(err)
	}
	if c.Default() != Official || !c.Exclusive() {
		t.Errorf("defaults: %s %v", c.Default(), c.Exclusive())
	}
	c.Settings.Set("", "default", "official-b11200")
	c.Settings.Set("", "exclusive", "false")
	c.Models.Set("a/b", "runtime", "x")
	if err := c.Save(); err != nil {
		t.Fatal(err)
	}
	c2, err := Load(d)
	if err != nil {
		t.Fatal(err)
	}
	if c2.Default() != "official-b11200" || c2.Exclusive() || len(c2.Mappings()) != 1 {
		t.Errorf("reloaded: %s %v %v", c2.Default(), c2.Exclusive(), c2.Mappings())
	}
	if _, err := os.Stat(filepath.Join(d.Config, "models.ini")); err != nil {
		t.Error(err)
	}
}

func TestLlamaenvHomePutsEverythingInOneFolder(t *testing.T) {
	t.Setenv("LLAMAENV_HOME", "/x/llamaenv")
	d, err := DefaultDirs()
	if err != nil || d.Config != "/x/llamaenv" || d.Data != "/x/llamaenv" || d.Bin() != filepath.Join("/x/llamaenv", "bin") {
		t.Errorf("%+v %v", d, err)
	}
}

func TestXDGDirsOnLinux(t *testing.T) {
	if os.PathSeparator != '/' {
		t.Skip("Linux layout")
	}
	t.Setenv("LLAMAENV_HOME", "")
	t.Setenv("XDG_CONFIG_HOME", "/c")
	t.Setenv("XDG_DATA_HOME", "/d")
	d, err := DefaultDirs()
	if err != nil || d.Config != "/c/llamaenv" || d.Data != "/d/llamaenv" || d.Logs() != "/d/llamaenv/logs" {
		t.Errorf("%+v %v", d, err)
	}
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("HOME", "/h")
	if d, _ := DefaultDirs(); d.Config != "/h/.config/llamaenv" || d.Data != "/h/.local/share/llamaenv" {
		t.Errorf("defaults %+v", d)
	}
}

func TestSectionKeysAndBrokenFiles(t *testing.T) {
	f, _ := ParseINI("[r]\na = 1\n; c\nb = 2\n")
	if k := f.Section("r").Keys(); len(k) != 2 || k[1] != [2]string{"b", "2"} {
		t.Errorf("keys %v", k)
	}
	if _, ok := f.Get("missing", "a"); ok {
		t.Error("value from a missing section")
	}
	d := Dirs{Config: t.TempDir(), Data: t.TempDir()}
	if err := os.WriteFile(filepath.Join(d.Config, "models.ini"), []byte("broken\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(d); err == nil || !strings.Contains(err.Error(), "models.ini") {
		t.Errorf("broken file: %v", err)
	}
	blocked := Dirs{Config: filepath.Join(t.TempDir(), "file")}
	if err := os.WriteFile(blocked.Config, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	c := &Config{Dirs: blocked, Settings: f, Runtimes: f, Models: f, Lock: f}
	if err := c.Save(); err == nil {
		t.Error("saved into a file instead of a folder")
	}
}

func TestDirsForEachSystem(t *testing.T) {
	env := func(vars map[string]string) func(string) string { return func(k string) string { return vars[k] } }
	d, err := dirsFor("windows", env(map[string]string{"LOCALAPPDATA": `C:\Users\u\AppData\Local`}), "")
	if err != nil || d.Config != d.Data || !strings.HasSuffix(d.Config, "llamaenv") {
		t.Errorf("windows: %+v %v", d, err)
	}
	if _, err := dirsFor("windows", env(nil), ""); err == nil {
		t.Error("windows without LOCALAPPDATA accepted")
	}
	if d, _ := dirsFor("linux", env(map[string]string{"LLAMAENV_HOME": "/p"}), "/h"); d.Config != "/p" || d.Data != "/p" {
		t.Errorf("LLAMAENV_HOME: %+v", d)
	}
}
