package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strings"
	"text/template"
	"time"
)

func home() string { h, _ := os.UserHomeDir(); return h }

func srcDir() string {
	if d := os.Getenv("HOMIE_SOURCE"); d != "" {
		return d
	}
	return filepath.Join(home(), ".local", "share", "homie")
}

func stateDir() string { return filepath.Join(home(), ".local", "state", "homie") }

func dirExists(p string) bool { _, err := os.Stat(p); return err == nil }

// machine names this box for ##variants, templates and package `only`:
// $HOMIE_MACHINE, else the saved name, else the config's machine_detect output, else the OS.
func machine() string {
	if m := os.Getenv("HOMIE_MACHINE"); m != "" {
		return m
	}
	if b, err := os.ReadFile(filepath.Join(stateDir(), "machine")); err == nil {
		return strings.TrimSpace(string(b))
	}
	if cfg, err := loadConfig(); err == nil && cfg.MachineDetect != "" {
		if out, err := shellOutput(cfg.MachineDetect); err == nil && strings.TrimSpace(out) != "" {
			m := strings.TrimSpace(out)
			_ = setMachine(m)
			return m
		}
	}
	return runtime.GOOS
}

func setMachine(m string) error {
	if err := os.MkdirAll(stateDir(), 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(stateDir(), "machine"), []byte(m+"\n"), 0o644)
}

func matches(tags []string, m string) bool {
	return slices.ContainsFunc(tags, func(t string) bool { return t == m || t == runtime.GOOS })
}

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
			b, err := os.ReadFile(filepath.Join(srcDir(), filepath.FromSlash(p)))
			return string(b), err
		},
		"sha256sum": func(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) },
	}).Parse(string(src))
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	err = t.Execute(&buf, map[string]any{
		"os": runtime.GOOS, "arch": runtime.GOARCH, "hostname": host, "machine": m, "home": home(),
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
	err = filepath.WalkDir(srcDir(), func(p string, d fs.DirEntry, err error) error {
		if err != nil || p == srcDir() {
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
		rel, _ := filepath.Rel(srcDir(), p)
		parts := strings.Split(filepath.ToSlash(rel), "/")
		e := entry{src: p}
		dst := home()
		for i, part := range parts {
			a := parseName(part, i == len(parts)-1)
			dst = filepath.Join(dst, a.name)
			if i < len(parts)-1 {
				if a.private {
					e.privDirs = append(e.privDirs, dst)
				}
				continue
			}
			if len(a.tags) > 0 && !matches(a.tags, m) {
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

func changed() ([]entry, error) {
	files, _, err := scan(machine())
	return slices.DeleteFunc(files, func(e entry) bool { return e.state == "" }), err
}

func rel(p string) string { r, _ := filepath.Rel(home(), p); return r }

func status() error {
	es, err := changed()
	for _, e := range es {
		fmt.Println(e.state, rel(e.dst))
	}
	if err == nil && len(es) == 0 {
		fmt.Println("everything up to date (machine:", machine()+")")
	}
	return err
}

func diff() error {
	es, err := changed()
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
		fmt.Println("---", rel(e.dst))
		c := exec.Command("git", "--no-pager", "diff", "--no-index", "--", old, src)
		c.Stdout, c.Stderr = os.Stdout, os.Stderr
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
	if err := os.WriteFile(e.dst, e.data, e.mode); err != nil {
		return err
	}
	return os.Chmod(e.dst, e.mode) // WriteFile keeps the old mode of an existing file
}

// apply writes changed files (backing up what it overwrites), installs missing packages, then runs scripts.
func apply() error {
	m := machine()
	files, scripts, err := scan(m)
	if err != nil {
		return err
	}
	backup := filepath.Join(stateDir(), "backup", time.Now().Format("20060102-150405"))
	backedUp := false
	for _, e := range files {
		if e.state == "" {
			continue
		}
		if have, err := os.ReadFile(e.dst); err == nil && !bytes.Equal(have, e.data) {
			b := filepath.Join(backup, rel(e.dst))
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
		fmt.Println("apply", rel(e.dst))
	}
	if backedUp {
		fmt.Println("overwritten files backed up to", backup)
	}
	if err := installMissing(); err != nil {
		return err
	}
	return runScripts(scripts)
}

// runScripts runs run_* every apply and run_onchange_* only when its rendered content changed.
func runScripts(scripts []entry) error {
	hashes := loadJSON("scripts.json")
	for _, s := range scripts {
		name := filepath.Base(s.dst)
		if (runtime.GOOS == "windows") != strings.HasSuffix(name, ".ps1") {
			continue // .ps1 scripts are for Windows, everything else for unix
		}
		sum := sha256.Sum256(s.data)
		h := hex.EncodeToString(sum[:])
		if strings.HasPrefix(name, "run_onchange_") && hashes[name] == h {
			continue
		}
		fmt.Println("run", name)
		f, err := os.CreateTemp("", "homie-*-"+name)
		if err != nil {
			return err
		}
		f.Write(s.data)
		f.Close()
		os.Chmod(f.Name(), 0o700)
		var c *exec.Cmd
		if runtime.GOOS == "windows" {
			c = exec.Command("powershell", "-NoProfile", "-ExecutionPolicy", "Bypass", "-File", f.Name())
		} else if bytes.HasPrefix(s.data, []byte("#!")) {
			c = exec.Command(f.Name())
		} else {
			c = exec.Command("sh", f.Name())
		}
		c.Dir, c.Stdin, c.Stdout, c.Stderr = home(), os.Stdin, os.Stdout, os.Stderr
		err = c.Run()
		os.Remove(f.Name())
		if err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		hashes[name] = h
		if err := saveJSON("scripts.json", hashes); err != nil {
			return err
		}
	}
	return nil
}

// sourcePath picks the source path for a home-relative target, reusing source names that already exist
// (so private_dot_config stays private_dot_config) and deriving attributes from the file otherwise.
func sourcePath(target, tag string) (string, error) {
	parts := strings.Split(filepath.ToSlash(target), "/")
	dir, tdir := srcDir(), home()
	for i, part := range parts {
		last := i == len(parts)-1
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

// add copies files under $HOME into the source dir; tag makes them a variant for that machine/OS.
func add(path, tag string) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	if r, err := filepath.Rel(home(), abs); err != nil || strings.HasPrefix(r, "..") || r == "." {
		return fmt.Errorf("%s is not under your home directory", path)
	}
	return filepath.WalkDir(abs, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		dst, err := sourcePath(rel(p), tag)
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
		r, _ := filepath.Rel(srcDir(), dst)
		fmt.Println("add", rel(p), "->", r)
		return os.WriteFile(dst, data, 0o644)
	})
}

func shell(script string) *exec.Cmd {
	if runtime.GOOS == "windows" {
		return exec.Command("powershell", "-NoProfile", "-ExecutionPolicy", "Bypass", "-Command", script)
	}
	return exec.Command("sh", "-c", script)
}

func shellOutput(script string) (string, error) {
	c := shell(script)
	c.Stderr = os.Stderr
	out, err := c.Output()
	return string(out), err
}
