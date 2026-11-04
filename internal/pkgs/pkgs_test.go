package pkgs

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/ryanzhangtianran/homie/internal/config"
)

func TestResolve(t *testing.T) {
	p := config.Pkg{GH: "a/b", Only: []string{"server", "darwin"}, On: map[string]config.Pkg{
		runtime.GOOS:                        {URL: "os"},
		runtime.GOOS + "/" + runtime.GOARCH: {URL: "os-arch"},
		"server":                            {Script: "srv"},
	}}
	if r, ok := config.Resolve("x", p, "server"); !ok || r.Script != "srv" || r.URL != "" || r.GH != "" {
		t.Fatalf("%+v %v", r, ok)
	}
	if r, _ := config.Resolve("x", p, "laptop"); r.URL != "os-arch" || r.GH != "" {
		t.Fatalf("%+v", r)
	}
	if _, ok := config.Resolve("x", config.Pkg{GH: "a/b", Only: []string{"nope"}}, "server"); ok {
		t.Fatal("only not honoured")
	}
	if got := expand("a-{version}-{tag}-{os}", "v1.2"); got != "a-1.2-v1.2-"+runtime.GOOS {
		t.Fatal(got)
	}
	if _, _, err := latest(config.Pkg{URL: "https://example.com/x-{version}.zip"}); err == nil {
		t.Fatal("{version} outside github must error")
	}
}

func TestConfigForms(t *testing.T) {
	_, src := sandbox(t, "server")
	write(t, filepath.Join(src, ".homie.toml"), config.Default, 0o644)
	ps, err := Packages()
	if err != nil || len(ps) != 6 {
		t.Fatalf("%v %v", ps, err)
	}
	got := map[string]config.Pkg{}
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
	if err := AddPkg("hello", `echo "hi" | cat`); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load()
	if err != nil || cfg.Packages["hello"].Script != `echo "hi" | cat` || cfg.Packages["claude"].Script != "x" {
		t.Fatalf("%+v %v\n%s", cfg.Packages, err, read(config.File()))
	}
	if err := AddPkg("fzf", "gh:x/y"); err == nil {
		t.Error("duplicate must fail")
	}
	tricky := "echo 'it''s'\necho \"done\""
	if err := AddPkg("tricky", tricky); err != nil {
		t.Fatal(err)
	}
	if cfg, _ := config.Load(); cfg.Packages["tricky"].Script != tricky {
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
	if !Installed(config.Pkg{Name: "uv", GH: "astral-sh/uv"}) {
		t.Fatal("uv should count as installed")
	}
	if err := Uninstall([]string{"uv", "x"}); err != nil {
		t.Fatal(err)
	}
	if config.Exists(filepath.Join(binDir(), exe("uvx"))) || config.Exists(filepath.Join(h, ".cache", "x")) || loadState()["uv"].Tag != "" {
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

func TestUpgradeSkipsInstalledScripts(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("sh command")
	}
	h, src := sandbox(t, "server")
	write(t, filepath.Join(src, ".homie.toml"), "[packages]\ntool = 'echo x >> \"$HOME/ran\"'\n", 0o644)
	write(t, filepath.Join(binDir(), "tool"), "", 0o755) // already installed
	if err := Install(nil); err != nil {
		t.Fatal(err)
	}
	if config.Exists(filepath.Join(h, "ran")) {
		t.Fatal("bulk upgrade reran an installed install-command package")
	}
	if err := Install([]string{"tool"}); err != nil {
		t.Fatal(err)
	}
	if read(filepath.Join(h, "ran")) != "x\n" {
		t.Fatal("naming the package must rerun its install command")
	}
}

func tarGz(t *testing.T, files map[string]int64) []byte {
	t.Helper()
	var b bytes.Buffer
	gz := gzip.NewWriter(&b)
	tw := tar.NewWriter(gz)
	for name, mode := range files {
		tw.WriteHeader(&tar.Header{Name: name, Mode: mode, Size: 1, Typeflag: tar.TypeReg})
		tw.Write([]byte("x"))
	}
	tw.Close()
	gz.Close()
	return b.Bytes()
}

// One failing download must not stop the next package, and a good one ends up in ~/.local/bin with its state.
func TestInstallFromURL(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("exec bits")
	}
	archive := tarGz(t, map[string]int64{"tool-1/tool": 0o755, "tool-1/README": 0o644})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/tool.tar.gz" {
			http.NotFound(w, r)
			return
		}
		w.Write(archive)
	}))
	defer srv.Close()
	_, src := sandbox(t, "server")
	write(t, filepath.Join(src, ".homie.toml"), fmt.Sprintf("[packages.aaa]\nurl = %q\n[packages.tool]\nurl = %q\n",
		srv.URL+"/gone.tar.gz", srv.URL+"/tool.tar.gz"), 0o644)
	err := Install(nil)
	if err == nil || !strings.Contains(err.Error(), "1 of 2") {
		t.Fatalf("want one failure reported, got %v", err)
	}
	fi, statErr := os.Stat(filepath.Join(binDir(), "tool"))
	if statErr != nil || fi.Mode()&0o111 == 0 {
		t.Fatal("tool not installed (or not executable) after the other package failed")
	}
	if got := loadState()["tool"].Bins; len(got) != 1 || got[0] != "tool" {
		t.Fatalf("state bins = %v", got)
	}
	if config.Exists(filepath.Join(binDir(), "README")) {
		t.Fatal("non-executable archive files must not be installed")
	}
}

func TestListAssetsFallsBackToAPI(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/repos/o/r/releases/tags/v1") {
			w.Write([]byte(`{"assets":[{"name":"r-linux.tar.gz"},{"name":"r-darwin.tar.gz"}]}`))
			return
		}
		w.Write([]byte("<html>GitHub changed this page</html>"))
	}))
	defer srv.Close()
	oldWeb, oldAPI := github, githubAPI
	github, githubAPI = srv.URL, srv.URL+"/api"
	defer func() { github, githubAPI = oldWeb, oldAPI }()
	names, err := listAssets("o/r", "v1")
	if err != nil || len(names) != 2 || names[0] != "r-linux.tar.gz" {
		t.Fatalf("%v %v", names, err)
	}
}

func TestScriptPackageBins(t *testing.T) {
	sandbox(t, "server")
	p := config.Pkg{Name: "rust", Script: "true", Bins: []string{"homie-test-cargo"}}
	if Installed(p) {
		t.Fatal("not installed yet")
	}
	write(t, filepath.Join(binDir(), exe("homie-test-cargo")), "", 0o755)
	if !Installed(p) {
		t.Fatal("bins[0] names the program to look for")
	}
}

func TestDamagedStateIsReported(t *testing.T) {
	sandbox(t, "server")
	write(t, filepath.Join(config.StateDir(), "packages.json"), `{"half": {"tag": "v1`, 0o644)
	if got := loadState(); len(got) != 0 {
		t.Fatalf("damaged state should read as empty, got %v", got)
	}
	if err := config.ReadJSON("packages.json", &map[string]pkgState{}); err == nil {
		t.Fatal("damaged state must be an error for the caller")
	}
	if err := saveState(map[string]pkgState{"a": {Tag: "v2"}}); err != nil || loadState()["a"].Tag != "v2" {
		t.Fatal(err)
	}
	if config.Exists(filepath.Join(config.StateDir(), "packages.json.tmp")) {
		t.Fatal("temp file left behind")
	}
}
