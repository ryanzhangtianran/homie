package dotfiles

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/ryanzhangtianran/homie/internal/config"
	"github.com/ryanzhangtianran/homie/internal/pkgs"
	"github.com/ryanzhangtianran/homie/internal/ui"
)

// attrs is one parsed source path component, chezmoi style: [private_][executable_][dot_]name[.tmpl][##tag,tag].
type attrs struct {
	name                string
	private, exec, tmpl bool
	tags                []string
}

func parseName(s string, file bool) attrs {
	var a attrs
	if file {
		if i := strings.Index(s, "##"); i >= 0 {
			a.tags, s = strings.Split(s[i+2:], ","), s[:i]
		}
		if t, ok := strings.CutSuffix(s, ".tmpl"); ok {
			a.tmpl, s = true, t
		}
	}
	if t, ok := strings.CutPrefix(s, "private_"); ok {
		a.private, s = true, t
	}
	if t, ok := strings.CutPrefix(s, "executable_"); ok && file {
		a.exec, s = true, t
	}
	if t, ok := strings.CutPrefix(s, "dot_"); ok {
		s = "." + t
	}
	a.name = s
	return a
}

func (a attrs) mode() fs.FileMode {
	m := fs.FileMode(0o644)
	if a.exec {
		m = 0o755
	}
	if a.private {
		m &= 0o700
	}
	return m
}

func render(name string, src []byte, m string) ([]byte, error) {
	host, _ := os.Hostname()
	t, err := template.New(name).Option("missingkey=error").Funcs(template.FuncMap{
		"include": func(p string) (string, error) {
			b, err := os.ReadFile(filepath.Join(config.SrcDir(), filepath.FromSlash(p)))
			return string(b), err
		},
		"sha256sum": func(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) },
	}).Parse(string(src))
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	err = t.Execute(&buf, map[string]any{
		"os": runtime.GOOS, "arch": runtime.GOARCH, "hostname": host, "machine": m, "home": config.Home(),
	})
	return buf.Bytes(), err
}

type entry struct {
	src, dst, state string // state: "A" missing in $HOME, "M" differs, "" same
	data            []byte
	mode            fs.FileMode
	tmpl            bool
	privDirs        []string // target dirs that must be 0700
}

// scan resolves the source dir for machine m: one entry per target (matching variant beats the
// default, other variants are dropped) plus the run_ scripts. Names starting with "." are metadata.
func scan(m string) (files []entry, scripts []entry, err error) {
	best, rank := map[string]entry{}, map[string]int{}
	err = filepath.WalkDir(config.SrcDir(), func(p string, d fs.DirEntry, err error) error {
		if err != nil || p == config.SrcDir() {
			return err
		}
		if strings.HasPrefix(d.Name(), ".") {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(config.SrcDir(), p)
		parts := strings.Split(filepath.ToSlash(rel), "/")
		e := entry{src: p}
		dst := config.Home()
		for i, part := range parts {
			a := parseName(part, i == len(parts)-1)
			dst = filepath.Join(dst, a.name)
			if i < len(parts)-1 {
				if a.private {
					e.privDirs = append(e.privDirs, dst)
				}
				continue
			}
			if len(a.tags) > 0 && !config.Matches(a.tags, m) {
				return nil
			}
			e.dst, e.mode, e.tmpl = dst, a.mode(), a.tmpl
			if e.data, err = os.ReadFile(p); err != nil {
				return err
			}
			if a.tmpl {
				if e.data, err = render(rel, e.data, m); err != nil {
					return fmt.Errorf("%s: %w", rel, err)
				}
			}
			if len(parts) == 1 && strings.HasPrefix(a.name, "run_") {
				scripts = append(scripts, e)
				return nil
			}
			r := 1
			if len(a.tags) > 0 {
				r = 2
			}
			if r > rank[dst] {
				best[dst], rank[dst] = e, r
			}
		}
		return nil
	})
	for _, e := range best {
		info, statErr := os.Stat(e.dst)
		switch {
		case statErr != nil:
			e.state = "A"
		case runtime.GOOS != "windows" && info.Mode().Perm() != e.mode:
			e.state = "M"
		default:
			if have, _ := os.ReadFile(e.dst); !bytes.Equal(have, e.data) {
				e.state = "M"
			}
		}
		files = append(files, e)
	}
	sort.Slice(files, func(i, j int) bool { return files[i].dst < files[j].dst })
	sort.Slice(scripts, func(i, j int) bool { return scripts[i].dst < scripts[j].dst })
	return files, scripts, err
}

func Changed() ([]entry, error) {
	files, _, err := scan(config.Machine())
	return slices.DeleteFunc(files, func(e entry) bool { return e.state == "" }), err
}

func Rel(p string) string { r, _ := filepath.Rel(config.Home(), p); return r }

func Status() error {
	es, err := Changed()
	if err != nil {
		return err
	}
	title := "Status · machine " + config.Machine()
	if len(es) == 0 {
		fmt.Fprintln(ui.Out, ui.Box(title, lipgloss.NewStyle().Foreground(ui.OK).Render("✓ everything up to date")))
		return nil
	}
	var rows [][]string
	for _, e := range es {
		st := lipgloss.NewStyle().Foreground(ui.OK).Render("+ new")
		if e.state == "M" {
			st = lipgloss.NewStyle().Foreground(ui.Warn).Render("~ changed")
		}
		rows = append(rows, []string{st, "~/" + Rel(e.dst)})
	}
	ui.Grid(title, []string{"STATE", "FILE"}, rows)
	return nil
}

func Diff() error {
	es, err := Changed()
	for _, e := range es {
		old, src := e.dst, e.src
		if e.state == "A" {
			old = os.DevNull
		}
		if e.tmpl { // diff the rendered output, not the template
			f, err := os.CreateTemp("", "homie-*")
			if err != nil {
				return err
			}
			f.Write(e.data)
			f.Close()
			defer os.Remove(f.Name())
			src = f.Name()
		}
		ui.Say(ui.Accent, "◆", "diff", "~/"+Rel(e.dst))
		color := "--color=auto"
		if ui.Out != io.Writer(os.Stdout) { // captured for the full-screen UI: keep the colors
			color = "--color=always"
		}
		c := exec.Command("git", "--no-pager", "diff", color, "--no-index", "--", old, src)
		c.Stdout, c.Stderr = ui.Out, ui.Out
		_ = c.Run() // exits 1 when files differ
	}
	return err
}

func writeFile(e entry) error {
	if err := os.MkdirAll(filepath.Dir(e.dst), 0o755); err != nil {
		return err
	}
	for _, d := range e.privDirs {
		if err := os.Chmod(d, 0o700); err != nil {
			return err
		}
	}
	if fi, err := os.Lstat(e.dst); err == nil && fi.Mode()&fs.ModeSymlink != 0 {
		// replace the link itself; writing through it would change the file it points to (stow-style setups)
		if err := os.Remove(e.dst); err != nil {
			return err
		}
	}
	if err := os.WriteFile(e.dst, e.data, e.mode); err != nil {
		return err
	}
	return os.Chmod(e.dst, e.mode) // WriteFile keeps the old mode of an existing file
}

// Apply writes changed files (backing up what it overwrites), installs missing packages, then runs scripts.
func Apply() error { return ApplyWith(true) }

// ApplyWith is apply; pkgs=false leaves packages alone (homie pull: dotfiles only).
func ApplyWith(withPkgs bool) error {
	m := config.Machine()
	files, scripts, err := scan(m)
	if err != nil {
		return err
	}
	backup := filepath.Join(config.StateDir(), "backup", time.Now().Format("20060102-150405"))
	backedUp := false
	for _, e := range files {
		if e.state == "" {
			continue
		}
		if have, err := os.ReadFile(e.dst); err == nil && !bytes.Equal(have, e.data) {
			b := filepath.Join(backup, Rel(e.dst))
			if err := os.MkdirAll(filepath.Dir(b), 0o755); err != nil {
				return err
			}
			if err := os.WriteFile(b, have, 0o600); err != nil {
				return err
			}
			backedUp = true
		}
		if err := writeFile(e); err != nil {
			return err
		}
		ui.Say(ui.OK, "✓", "apply", "~/"+Rel(e.dst))
	}
	if backedUp {
		ui.Say(ui.Muted, "↺", "backup", "overwritten files saved in "+backup)
	}
	var pkgErr error
	if withPkgs { // a failed package must not keep the scripts from running
		pkgErr = pkgs.InstallMissing()
	}
	return errors.Join(pkgErr, runScripts(scripts))
}

// reserved reports names that would not survive the trip into the source repo and back:
// ~/run_x.sh would be executed by apply instead of copied, ~/private_notes would come back as ~/notes.
func reserved(name string, file, top bool) bool {
	for _, p := range []string{"dot_", "private_", "executable_"} {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return strings.Contains(name, "##") || file && strings.HasSuffix(name, ".tmpl") || file && top && strings.HasPrefix(name, "run_")
}

// sourcePath picks the source path for a home-relative target, reusing source names that already exist
// (so private_dot_config stays private_dot_config) and deriving attributes from the file otherwise.
func sourcePath(target, tag string) (string, error) {
	parts := strings.Split(filepath.ToSlash(target), "/")
	dir, tdir := config.SrcDir(), config.Home()
	for i, part := range parts {
		last := i == len(parts)-1
		if reserved(part, last, i == 0) {
			return "", fmt.Errorf("can't manage %s: %q would be read as a homie source name "+
				"(dot_/private_/executable_ prefixes, ##tags, .tmpl, or run_ scripts at the top)", target, part)
		}
		tdir = filepath.Join(tdir, part)
		info, err := os.Stat(tdir)
		if err != nil {
			return "", err
		}
		existing, _ := os.ReadDir(dir)
		found := ""
		for _, de := range existing {
			a := parseName(de.Name(), last)
			if a.name == part && de.IsDir() == !last && strings.Join(a.tags, ",") == tag {
				if a.tmpl {
					return "", fmt.Errorf("%s is a template: edit %s instead", target, filepath.Join(dir, de.Name()))
				}
				found = de.Name()
			}
		}
		if found == "" {
			found = part
			if t, ok := strings.CutPrefix(part, "."); ok {
				found = "dot_" + t
			}
			if runtime.GOOS != "windows" {
				if last && info.Mode().Perm()&0o111 != 0 {
					found = "executable_" + found
				}
				if info.Mode().Perm()&0o077 == 0 {
					found = "private_" + found
				}
			}
			if last && tag != "" {
				found += "##" + tag
			}
		}
		dir = filepath.Join(dir, found)
	}
	return dir, nil
}

// Add copies files under $HOME into the source dir; tag makes them a variant for that machine/OS.
func Add(path, tag string) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	if r, err := filepath.Rel(config.Home(), abs); err != nil || strings.HasPrefix(r, "..") || r == "." {
		return fmt.Errorf("%s is not under your home directory", path)
	}
	return filepath.WalkDir(abs, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		dst, err := sourcePath(Rel(p), tag)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		r, _ := filepath.Rel(config.SrcDir(), dst)
		ui.Say(ui.OK, "+", "add", "~/"+Rel(p)+ui.Dim.Render("  →  "+r))
		return os.WriteFile(dst, data, 0o644)
	})
}
