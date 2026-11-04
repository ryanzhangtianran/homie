package config

import (
	"bytes"
	_ "embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"github.com/BurntSushi/toml"
)

//go:embed default.toml
var Default string

// Pkg is one entry of [packages]. Usually a single line:
//
//	name = "gh:owner/repo"            latest GitHub release, asset for this platform picked automatically
//	name = "curl -fsSL ... | bash"    any other string is an install command
//
// The table form [packages.name] adds the fields below. Everything installs under $HOME, no sudo.
type Pkg struct {
	Name   string            `toml:"-"`
	Script string            // install command
	GH     string            `toml:"gh"` // owner/repo
	URL    string            // explicit archive/binary url; placeholders: see expand
	Bins   []string          // executables to take from the archive (default: all of them)
	Files  map[string]string // archive member -> home-relative destination
	Remove []string          // extra home-relative paths uninstall deletes
	Only   []string          // machines/OSes to install on (default: all)
	On     map[string]Pkg    // overrides keyed by "<os>", "<os>/<arch>" or "<machine>"; values may be one-liners too
}

func parseSpec(s string) Pkg {
	if r, ok := strings.CutPrefix(s, "gh:"); ok {
		return Pkg{GH: r}
	}
	return Pkg{Script: s}
}

// UnmarshalTOML accepts both the one-line string and the table form.
func (p *Pkg) UnmarshalTOML(v any) error {
	if s, ok := v.(string); ok {
		*p = parseSpec(s)
		return nil
	}
	var buf bytes.Buffer
	if err := toml.NewEncoder(&buf).Encode(v); err != nil {
		return err
	}
	type plain Pkg // same fields, without this method
	var q plain
	if _, err := toml.Decode(buf.String(), &q); err != nil {
		return err
	}
	*p = Pkg(q)
	return nil
}

func (p Pkg) Spec() string {
	switch {
	case p.GH != "":
		return "gh:" + p.GH
	case p.URL != "":
		return p.URL
	}
	return p.Script
}

type Config struct {
	MachineDetect string `toml:"machine_detect"`
	Packages      map[string]Pkg
}

func File() string { return filepath.Join(SrcDir(), ".homie.toml") }

func Load() (Config, error) {
	var c Config
	_, err := toml.DecodeFile(File(), &c)
	if errors.Is(err, fs.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return c, fmt.Errorf("%s: %w", File(), err)
	}
	return c, nil
}

// Resolve merges the overrides matching this platform and machine into p.
func Resolve(name string, p Pkg, m string) (Pkg, bool) {
	for _, k := range []string{runtime.GOOS, runtime.GOOS + "/" + runtime.GOARCH, m} {
		o, ok := p.On[k]
		if !ok {
			continue
		}
		if o.Script != "" || o.GH != "" || o.URL != "" {
			p.Script, p.GH, p.URL = o.Script, o.GH, o.URL
		}
		if o.Bins != nil {
			p.Bins = o.Bins
		}
		if o.Files != nil {
			p.Files = o.Files
		}
		if o.Remove != nil {
			p.Remove = o.Remove
		}
	}
	p.Name = name
	return p, (len(p.Only) == 0 || Matches(p.Only, m)) && p.Spec() != ""
}

// Machine names this box for ##variants, templates and package `only`:
// $HOMIE_MACHINE, else the saved name, else the config's machine_detect output, else the OS.
func Machine() string {
	if m := os.Getenv("HOMIE_MACHINE"); m != "" {
		return m
	}
	if b, err := os.ReadFile(filepath.Join(StateDir(), "machine")); err == nil {
		return strings.TrimSpace(string(b))
	}
	if cfg, err := Load(); err == nil && cfg.MachineDetect != "" {
		if out, err := shellOutput(cfg.MachineDetect); err == nil && strings.TrimSpace(out) != "" {
			m := strings.TrimSpace(out)
			_ = SetMachine(m)
			return m
		}
	}
	return runtime.GOOS
}

func SetMachine(m string) error {
	if err := os.MkdirAll(StateDir(), 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(StateDir(), "machine"), []byte(m+"\n"), 0o644)
}

func Matches(tags []string, m string) bool {
	return slices.ContainsFunc(tags, func(t string) bool { return t == m || t == runtime.GOOS })
}
