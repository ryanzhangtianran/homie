package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

// sandbox gives each test its own $HOME and source dir.
func sandbox(t *testing.T, machineName string) (h, src string) {
	h = t.TempDir()
	src = filepath.Join(h, "src")
	t.Setenv("HOME", h)
	t.Setenv("USERPROFILE", h)
	t.Setenv("HOMIE_SOURCE", src)
	t.Setenv("HOMIE_MACHINE", machineName)
	os.MkdirAll(src, 0o755)
	return
}

func write(t *testing.T, p, s string, mode os.FileMode) {
	t.Helper()
	os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.WriteFile(p, []byte(s), mode); err != nil {
		t.Fatal(err)
	}
}

func read(p string) string { b, _ := os.ReadFile(p); return string(b) }

func TestParseName(t *testing.T) {
	a := parseName("private_executable_dot_foo.sh.tmpl##server,mac", true)
	if a.name != ".foo.sh" || !a.private || !a.exec || !a.tmpl || strings.Join(a.tags, ",") != "server,mac" {
		t.Fatalf("%+v", a)
	}
	if a.mode() != 0o700 {
		t.Fatalf("mode %o", a.mode())
	}
	if d := parseName("private_dot_config", false); d.name != ".config" || !d.private {
		t.Fatalf("%+v", d)
	}
}

func TestApplyVariantsTemplatesModes(t *testing.T) {
	h, src := sandbox(t, "server")
	write(t, filepath.Join(src, "dot_bashrc"), "default", 0o644)
	write(t, filepath.Join(src, "dot_bashrc##server"), "server", 0o644)
	write(t, filepath.Join(src, "dot_zshrc##mac"), "mac only", 0o644)
	write(t, filepath.Join(src, "dot_profile"), "plain", 0o644)
	write(t, filepath.Join(src, "dot_env.tmpl"), `m={{ .machine }}{{ if eq .machine "server" "desktop" }} proxy{{ end }}`, 0o644)
	write(t, filepath.Join(src, "private_dot_config", "app", "executable_run.sh"), "#!/bin/sh", 0o644)
	write(t, filepath.Join(src, ".homie.toml"), "", 0o644)
	write(t, filepath.Join(h, ".profile"), "local edit", 0o644)

	if err := apply(); err != nil {
		t.Fatal(err)
	}
	if got := read(filepath.Join(h, ".bashrc")); got != "server" {
		t.Errorf("variant not chosen: %q", got)
	}
	if dirExists(filepath.Join(h, ".zshrc")) {
		t.Error("mac-only file applied on server")
	}
	if dirExists(filepath.Join(h, ".homie.toml")) {
		t.Error("metadata copied into home")
	}
	if got := read(filepath.Join(h, ".env")); got != "m=server proxy" {
		t.Errorf("template = %q", got)
	}
	backups, _ := filepath.Glob(filepath.Join(h, ".local", "state", "homie", "backup", "*", ".profile"))
	if len(backups) != 1 || read(backups[0]) != "local edit" {
		t.Error("overwritten file not backed up")
	}
	if runtime.GOOS != "windows" {
		if fi, _ := os.Stat(filepath.Join(h, ".config")); fi.Mode().Perm() != 0o700 {
			t.Errorf("private dir mode %o", fi.Mode().Perm())
		}
		if fi, _ := os.Stat(filepath.Join(h, ".config", "app", "run.sh")); fi.Mode().Perm() != 0o755 {
			t.Errorf("executable mode %o", fi.Mode().Perm())
		}
	}
	if es, err := changed(); err != nil || len(es) != 0 {
		t.Fatalf("not clean after apply: %v %v", es, err)
	}

	t.Setenv("HOMIE_MACHINE", "laptop")   // no ##laptop variant: the default wins
	if es, _ := changed(); len(es) != 2 { // .bashrc -> default, .env re-rendered
		t.Fatalf("changed on laptop = %v", es)
	}
}

func TestAddNaming(t *testing.T) {
	h, src := sandbox(t, "server")
	write(t, filepath.Join(src, "private_dot_config", "keep"), "", 0o644)
	write(t, filepath.Join(h, ".config", "starship.toml"), "x", 0o644) // ~/.config itself is 0755 here
	write(t, filepath.Join(h, ".bashrc"), "srv", 0o644)
	write(t, filepath.Join(h, "bin", "tool"), "#!/bin/sh", 0o755)
	write(t, filepath.Join(h, ".ssh_cfg"), "secret", 0o600)
	for _, c := range []struct{ path, tag string }{{".config/starship.toml", ""}, {".bashrc", "server"}, {"bin/tool", ""}, {".ssh_cfg", ""}} {
		if err := add(filepath.Join(h, c.path), c.tag); err != nil {
			t.Fatal(err)
		}
	}
	want := []string{"private_dot_config/starship.toml", "dot_bashrc##server"} // existing source dir reused
	if runtime.GOOS != "windows" {
		want = append(want, "bin/executable_tool", "private_dot_ssh_cfg")
	}
	for _, w := range want {
		if !dirExists(filepath.Join(src, filepath.FromSlash(w))) {
			t.Errorf("missing %s", w)
		}
	}
	write(t, filepath.Join(src, "dot_gitconfig.tmpl"), "{{ .os }}", 0o644)
	write(t, filepath.Join(h, ".gitconfig"), "x", 0o644)
	if err := add(filepath.Join(h, ".gitconfig"), ""); err == nil {
		t.Error("add must not overwrite a template")
	}
}

func TestRunScripts(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix scripts")
	}
	h, src := sandbox(t, "server")
	write(t, filepath.Join(src, "run_onchange_once.sh"), "echo x >> \"$HOME/once\"", 0o644)
	write(t, filepath.Join(src, "run_every.sh"), "echo x >> \"$HOME/every\"", 0o644)
	write(t, filepath.Join(src, "run_mac.sh##mac"), "touch \"$HOME/mac\"", 0o644)
	for range 2 {
		if err := apply(); err != nil {
			t.Fatal(err)
		}
	}
	if read(filepath.Join(h, "once")) != "x\n" || read(filepath.Join(h, "every")) != "x\nx\n" || dirExists(filepath.Join(h, "mac")) {
		t.Fatalf("once=%q every=%q", read(filepath.Join(h, "once")), read(filepath.Join(h, "every")))
	}
	if dirExists(filepath.Join(h, "run_every.sh")) {
		t.Error("script copied into home")
	}
}

func TestResolve(t *testing.T) {
	p := Pkg{GH: "a/b", Only: []string{"server", "darwin"}, On: map[string]Pkg{
		runtime.GOOS:                        {URL: "os"},
		runtime.GOOS + "/" + runtime.GOARCH: {URL: "os-arch"},
		"server":                            {Script: "srv"},
	}}
	if r, ok := resolve("x", p, "server"); !ok || r.Script != "srv" || r.URL != "" || r.GH != "" {
		t.Fatalf("%+v %v", r, ok)
	}
	if r, _ := resolve("x", p, "laptop"); r.URL != "os-arch" || r.GH != "" {
		t.Fatalf("%+v", r)
	}
	if _, ok := resolve("x", Pkg{GH: "a/b", Only: []string{"nope"}}, "server"); ok {
		t.Fatal("only not honoured")
	}
	if got := expand("a-{version}-{tag}-{os}", "v1.2"); got != "a-1.2-v1.2-"+runtime.GOOS {
		t.Fatal(got)
	}
	if _, _, err := latest(Pkg{URL: "https://example.com/x-{version}.zip"}); err == nil {
		t.Fatal("{version} outside github must error")
	}
}

func TestConfigForms(t *testing.T) {
	_, src := sandbox(t, "server")
	write(t, filepath.Join(src, ".homie.toml"), defaultConfig, 0o644)
	ps, err := packages()
	if err != nil || len(ps) != 6 {
		t.Fatalf("%v %v", ps, err)
	}
	got := map[string]Pkg{}
	for _, p := range ps {
		got[p.Name] = p
	}
	if got["fzf"].GH != "junegunn/fzf" {
		t.Errorf("one-line gh: %+v", got["fzf"])
	}
	wantClaude := "curl -fsSL https://claude.ai/install.sh | bash"
	if runtime.GOOS == "windows" {
		wantClaude = "irm https://claude.ai/install.ps1 | iex" // one-line override inside on.windows
	}
	if got["claude"].Script != wantClaude || got["claude"].Remove[0] != ".local/share/claude" {
		t.Errorf("table form: %+v", got["claude"])
	}
}

func TestAddPkg(t *testing.T) {
	_, src := sandbox(t, "server")
	write(t, filepath.Join(src, ".homie.toml"), "machine_detect = 'echo x'\n\n[packages]\nfzf = \"gh:junegunn/fzf\"\n\n[packages.claude]\nscript = \"x\"\n", 0o644)
	// the install step just runs the echo; the config edit is what we check
	if err := addPkg("hello", `echo "hi" | cat`); err != nil {
		t.Fatal(err)
	}
	cfg, err := loadConfig()
	if err != nil || cfg.Packages["hello"].Script != `echo "hi" | cat` || cfg.Packages["claude"].Script != "x" {
		t.Fatalf("%+v %v\n%s", cfg.Packages, err, read(configFile()))
	}
	if err := addPkg("fzf", "gh:x/y"); err == nil {
		t.Error("duplicate must fail")
	}
	tricky := "echo 'it''s'\necho \"done\""
	if err := addPkg("tricky", tricky); err != nil {
		t.Fatal(err)
	}
	if cfg, _ := loadConfig(); cfg.Packages["tricky"].Script != tricky {
		t.Errorf("quotes/newlines not preserved: %q", cfg.Packages["tricky"].Script)
	}
}

func TestUninstall(t *testing.T) {
	h, src := sandbox(t, "server")
	write(t, filepath.Join(src, ".homie.toml"), "[packages]\nuv = \"gh:astral-sh/uv\"\n[packages.x]\nscript = \"true\"\nremove = [\".cache/x\"]\n", 0o644)
	for _, p := range []string{".local/bin/" + exe("uv"), ".local/bin/" + exe("uvx"), ".cache/x/y"} {
		write(t, filepath.Join(h, p), "x", 0o755)
	}
	saveState(map[string]pkgState{"uv": {Tag: "1", Bins: []string{exe("uv"), exe("uvx")}}})
	if !installed(Pkg{Name: "uv", GH: "astral-sh/uv"}) {
		t.Fatal("uv should count as installed")
	}
	if err := uninstall([]string{"uv", "x"}); err != nil {
		t.Fatal(err)
	}
	if dirExists(filepath.Join(binDir(), exe("uvx"))) || dirExists(filepath.Join(h, ".cache", "x")) || loadState()["uv"].Tag != "" {
		t.Fatal("not fully removed")
	}
}

func TestExecutables(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("exec bits")
	}
	var tb bytes.Buffer
	gz := gzip.NewWriter(&tb)
	tw := tar.NewWriter(gz)
	for _, f := range []struct {
		name string
		mode int64
	}{{"uv-x/uv", 0o755}, {"uv-x/uvx", 0o755}, {"uv-x/README.md", 0o644}} {
		tw.WriteHeader(&tar.Header{Name: f.name, Mode: f.mode, Size: 1, Typeflag: tar.TypeReg})
		tw.Write([]byte("x"))
	}
	tw.Close()
	gz.Close()
	got, err := executables("a.tar.gz", tb.Bytes(), "uv")
	if err != nil || len(got) != 2 || got["uv"] == nil || got["uvx"] == nil {
		t.Fatalf("%v %v", got, err)
	}
	if got, _ := executables("https://x/tool_linux_amd64", []byte("bin"), "tool"); string(got["tool"]) != "bin" {
		t.Fatal("bare binary")
	}
}

func TestExtract(t *testing.T) {
	var zb bytes.Buffer
	zw := zip.NewWriter(&zb)
	w, _ := zw.Create("dir/fnm")
	w.Write([]byte("zipbin"))
	zw.Close()
	if b, err := extract("a.zip", zb.Bytes(), "fnm"); err != nil || string(b) != "zipbin" {
		t.Fatalf("zip: %q %v", b, err)
	}
	var tb bytes.Buffer
	gz := gzip.NewWriter(&tb)
	tw := tar.NewWriter(gz)
	tw.WriteHeader(&tar.Header{Name: "rg-1/rg", Mode: 0o755, Size: 6, Typeflag: tar.TypeReg})
	tw.Write([]byte("tarbin"))
	tw.Close()
	gz.Close()
	if b, err := extract("a.tar.gz", tb.Bytes(), "rg"); err != nil || string(b) != "tarbin" {
		t.Fatalf("tar: %q %v", b, err)
	}
	if _, err := extract("a.tar.gz", tb.Bytes(), "missing"); err == nil {
		t.Fatal("expected not found")
	}
}

func TestSplitBranch(t *testing.T) {
	for in, want := range map[string][2]string{
		"me/dots@homie":                    {"me/dots", "homie"},
		"me/dots":                          {"me/dots", ""},
		"git@github.com:me/dots.git":       {"git@github.com:me/dots.git", ""},
		"git@github.com:me/dots.git@homie": {"git@github.com:me/dots.git", "homie"},
	} {
		if r, b := splitBranch(in); r != want[0] || b != want[1] {
			t.Errorf("splitBranch(%q) = %q, %q", in, r, b)
		}
	}
}

func TestRepoURL(t *testing.T) {
	for in, want := range map[string]string{
		"me/dotfiles":                    "https://github.com/me/dotfiles.git",
		"git@github.com:me/dotfiles.git": "git@github.com:me/dotfiles.git",
		"https://gitlab.com/me/dots.git": "https://gitlab.com/me/dots.git",
	} {
		if got := repoURL(in); got != want {
			t.Errorf("repoURL(%q) = %q", in, got)
		}
	}
}

func TestColumns(t *testing.T) {
	out := columns([]string{"A", "LONG HEADER"}, [][]string{{dim.Render("x"), "1"}, {"yyyy", "2"}, {"z", "last"}})
	lines := strings.Split(out, "\n")
	if len(lines) != 5 || !strings.Contains(lines[4], "last") { // header + rule + 3 rows, last row kept
		t.Fatalf("%q", lines)
	}
	col := func(l string) int { return lipgloss.Width(l) - len(strings.TrimLeft(stripANSI(l)[4:], " ")) }
	if col(lines[2]) != col(lines[3]) || col(lines[3]) != col(lines[4]) {
		t.Fatalf("second column not aligned:\n%s", out)
	}
}

func stripANSI(s string) string { return regexp.MustCompile(`\x1b\[[0-9;]*m`).ReplaceAllString(s, "") }

func TestHeader(t *testing.T) {
	_, src := sandbox(t, "server")
	write(t, filepath.Join(src, ".homie.toml"), "[packages]\nx = \"gh:a/b\"\n", 0o644)
	out := stripANSI(header())
	for _, want := range []string{"machine", "server", "source", "remote", "not bound", "files", "up to date", "packages", "0/1 installed"} {
		if !strings.Contains(out, want) {
			t.Errorf("header missing %q:\n%s", want, out)
		}
	}
}
