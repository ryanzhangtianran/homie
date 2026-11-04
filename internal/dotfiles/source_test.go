package dotfiles

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/ryanzhangtianran/homie/internal/config"
)

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

	if err := Apply(); err != nil {
		t.Fatal(err)
	}
	if got := read(filepath.Join(h, ".bashrc")); got != "server" {
		t.Errorf("variant not chosen: %q", got)
	}
	if config.Exists(filepath.Join(h, ".zshrc")) {
		t.Error("mac-only file applied on server")
	}
	if config.Exists(filepath.Join(h, ".homie.toml")) {
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
	if es, err := Changed(); err != nil || len(es) != 0 {
		t.Fatalf("not clean after apply: %v %v", es, err)
	}

	t.Setenv("HOMIE_MACHINE", "laptop")   // no ##laptop variant: the default wins
	if es, _ := Changed(); len(es) != 2 { // .bashrc -> default, .env re-rendered
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
		if err := Add(filepath.Join(h, c.path), c.tag); err != nil {
			t.Fatal(err)
		}
	}
	want := []string{"private_dot_config/starship.toml", "dot_bashrc##server"} // existing source dir reused
	if runtime.GOOS != "windows" {
		want = append(want, "bin/executable_tool", "private_dot_ssh_cfg")
	}
	for _, w := range want {
		if !config.Exists(filepath.Join(src, filepath.FromSlash(w))) {
			t.Errorf("missing %s", w)
		}
	}
	write(t, filepath.Join(src, "dot_gitconfig.tmpl"), "{{ .os }}", 0o644)
	write(t, filepath.Join(h, ".gitconfig"), "x", 0o644)
	if err := Add(filepath.Join(h, ".gitconfig"), ""); err == nil {
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
		if err := Apply(); err != nil {
			t.Fatal(err)
		}
	}
	if read(filepath.Join(h, "once")) != "x\n" || read(filepath.Join(h, "every")) != "x\nx\n" || config.Exists(filepath.Join(h, "mac")) {
		t.Fatalf("once=%q every=%q", read(filepath.Join(h, "once")), read(filepath.Join(h, "every")))
	}
	if config.Exists(filepath.Join(h, "run_every.sh")) {
		t.Error("script copied into home")
	}
}

func TestPullLeavesPackagesAlone(t *testing.T) {
	h, src := sandbox(t, "server")
	write(t, filepath.Join(src, ".homie.toml"), "[packages]\ntool = 'echo x >> \"$HOME/ran\"'\n", 0o644)
	write(t, filepath.Join(src, "dot_rc"), "v", 0o644)
	if err := ApplyWith(false); err != nil {
		t.Fatal(err)
	}
	if read(filepath.Join(h, ".rc")) != "v" || config.Exists(filepath.Join(h, "ran")) {
		t.Fatal("pull must write files and skip packages")
	}
}
