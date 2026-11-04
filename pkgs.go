package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
)

//go:embed default.toml
var defaultConfig string

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

func (p Pkg) spec() string {
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

func configFile() string { return filepath.Join(srcDir(), ".homie.toml") }

func loadConfig() (Config, error) {
	var c Config
	_, err := toml.DecodeFile(configFile(), &c)
	if errors.Is(err, fs.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return c, fmt.Errorf("%s: %w", configFile(), err)
	}
	return c, nil
}

// resolve merges the overrides matching this platform and machine into p.
func resolve(name string, p Pkg, m string) (Pkg, bool) {
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
	return p, (len(p.Only) == 0 || matches(p.Only, m)) && p.spec() != ""
}

// packages returns the packages that apply to this machine, sorted by name.
func packages() ([]Pkg, error) {
	cfg, err := loadConfig()
	m := machine()
	var out []Pkg
	for name, p := range cfg.Packages {
		if r, ok := resolve(name, p, m); ok {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, err
}

func findPkgs(names []string) ([]Pkg, error) {
	all, err := packages()
	if err != nil || len(names) == 0 {
		return all, err
	}
	var out []Pkg
	for _, n := range names {
		i := slices.IndexFunc(all, func(p Pkg) bool { return p.Name == n })
		if i < 0 {
			return nil, fmt.Errorf("%q is not in %s for this machine", n, configFile())
		}
		out = append(out, all[i])
	}
	return out, nil
}

func binDir() string { return filepath.Join(home(), ".local", "bin") }

func exe(name string) string {
	if runtime.GOOS == "windows" && !strings.HasSuffix(name, ".exe") {
		return name + ".exe"
	}
	return name
}

// pkgState is what homie remembers about an installed package (~/.local/state/homie/packages.json).
type pkgState struct {
	Tag  string   `json:"tag,omitempty"`
	Bins []string `json:"bins,omitempty"`
}

func loadState() map[string]pkgState {
	m := map[string]pkgState{}
	data, _ := os.ReadFile(filepath.Join(stateDir(), "packages.json"))
	_ = json.Unmarshal(data, &m)
	return m
}

func saveState(m map[string]pkgState) error {
	data, _ := json.MarshalIndent(m, "", "  ")
	if err := os.MkdirAll(stateDir(), 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(stateDir(), "packages.json"), data, 0o644)
}

func loadJSON(name string) map[string]string {
	m := map[string]string{}
	data, _ := os.ReadFile(filepath.Join(stateDir(), name))
	_ = json.Unmarshal(data, &m)
	return m
}

func saveJSON(name string, m map[string]string) error {
	data, _ := json.MarshalIndent(m, "", "  ")
	if err := os.MkdirAll(stateDir(), 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(stateDir(), name), data, 0o644)
}

// bins lists the executables p owns in ~/.local/bin: configured ones, else what the last install put there.
func (p Pkg) bins(st map[string]pkgState) []string {
	if len(p.Bins) > 0 {
		return p.Bins
	}
	return st[p.Name].Bins
}

func installed(p Pkg) bool {
	if p.Script != "" { // installed by the vendor's script, wherever it puts things
		_, err := exec.LookPath(p.Name)
		return err == nil || dirExists(filepath.Join(binDir(), exe(p.Name)))
	}
	bins := p.bins(loadState())
	for _, b := range bins {
		if !dirExists(filepath.Join(binDir(), exe(b))) {
			return false
		}
	}
	for _, dst := range p.Files {
		if !dirExists(filepath.Join(home(), dst)) {
			return false
		}
	}
	return len(bins) > 0
}

func rustArch() string {
	return map[string]string{"amd64": "x86_64", "arm64": "aarch64"}[runtime.GOARCH]
}

// expand fills url placeholders: {os} {arch} (amd64/arm64) {rust} (x86_64/aarch64) {ext} (.exe on
// Windows) and, for GitHub releases, {tag} (v1.2.3) and {version} (1.2.3).
func expand(s, tag string) string {
	ext := ""
	if runtime.GOOS == "windows" {
		ext = ".exe"
	}
	return strings.NewReplacer("{os}", runtime.GOOS, "{arch}", runtime.GOARCH, "{rust}", rustArch(),
		"{ext}", ext, "{tag}", tag, "{version}", strings.TrimPrefix(tag, "v")).Replace(s)
}

var ghRelease = regexp.MustCompile(`^https://github\.com/([^/]+/[^/]+)/releases/`)

// latestTag reads the tag from GitHub's /releases/latest redirect (no API token, no rate limit).
func latestTag(repo string) (string, error) {
	c := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := c.Head("https://github.com/" + repo + "/releases/latest")
	if err != nil {
		return "", err
	}
	resp.Body.Close()
	loc := resp.Header.Get("Location")
	if !strings.Contains(loc, "/releases/tag/") {
		return "", fmt.Errorf("no release found for %s", repo)
	}
	return path.Base(loc), nil
}

// latest returns the latest tag ("" when unknown) and the url to download for p.
func latest(p Pkg) (tag, url string, err error) {
	if p.GH != "" {
		if tag, err = latestTag(p.GH); err != nil {
			return "", "", err
		}
		return tag, "", nil // the asset is picked only when actually downloading
	}
	m := ghRelease.FindStringSubmatch(p.URL)
	if m == nil {
		if strings.Contains(p.URL, "{version}") || strings.Contains(p.URL, "{tag}") {
			return "", "", fmt.Errorf("{version}/{tag} only work in github.com release urls: %s", p.URL)
		}
		return "", expand(p.URL, ""), nil
	}
	if tag, err = latestTag(m[1]); err != nil {
		return "", "", err
	}
	u := strings.Replace(p.URL, "/releases/latest/download/", "/releases/download/{tag}/", 1)
	return tag, expand(u, tag), nil
}

func fetch(url string) ([]byte, error) {
	fmt.Println("downloading", url)
	resp, err := http.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("%s: %s", url, resp.Status)
	}
	return io.ReadAll(resp.Body)
}

// installOne installs p, or upgrades it when a newer release exists. Install commands are simply rerun.
func installOne(p Pkg) error {
	if p.Script != "" {
		fmt.Println("running installer for", p.Name)
		c := shell(p.Script)
		c.Env = append(os.Environ(), "PATH="+binDir()+string(os.PathListSeparator)+os.Getenv("PATH"))
		c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
		return c.Run()
	}
	st := loadState()
	tag, url, err := latest(p)
	if err != nil {
		return err
	}
	if tag != "" && tag == st[p.Name].Tag && installed(p) {
		fmt.Println(p.Name, tag, "is up to date")
		return nil
	}
	if p.GH != "" {
		names, err := listAssets(p.GH, tag)
		if err != nil {
			return err
		}
		asset, err := pickAsset(names, runtime.GOOS, runtime.GOARCH)
		if err != nil {
			return err
		}
		url = "https://github.com/" + p.GH + "/releases/download/" + tag + "/" + asset
	}
	data, err := fetch(url)
	if err != nil {
		return err
	}
	bins := map[string][]byte{}
	if len(p.Bins) > 0 {
		for _, b := range p.Bins {
			if bins[exe(b)], err = extract(url, data, exe(b)); err != nil {
				return err
			}
		}
	} else if bins, err = executables(url, data, exe(p.Name)); err != nil {
		return err
	}
	var names []string
	for name, b := range bins {
		if err := replaceFile(filepath.Join(binDir(), name), b, 0o755); err != nil {
			return err
		}
		names = append(names, name)
	}
	for member, dst := range p.Files {
		b, err := extract(url, data, member)
		if err != nil {
			return err
		}
		if err := replaceFile(filepath.Join(home(), filepath.FromSlash(dst)), b, 0o644); err != nil {
			return err
		}
	}
	sort.Strings(names)
	fmt.Println("installed", p.Name, tag, strings.Join(names, " "))
	st[p.Name] = pkgState{Tag: tag, Bins: names}
	return saveState(st)
}

func install(names []string) error {
	ps, err := findPkgs(names)
	if err != nil {
		return err
	}
	for _, p := range ps {
		if err := installOne(p); err != nil {
			return fmt.Errorf("%s: %w", p.Name, err)
		}
	}
	if !slices.Contains(filepath.SplitList(os.Getenv("PATH")), binDir()) {
		fmt.Printf("note: add %s to your PATH\n", binDir())
	}
	return nil
}

// installMissing is the apply step: only packages not yet present, so apply stays offline when nothing is missing.
func installMissing() error {
	ps, err := packages()
	if err != nil {
		return err
	}
	var missing []string
	for _, p := range ps {
		if !installed(p) {
			missing = append(missing, p.Name)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	return install(missing)
}

var bareKey = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
var packagesHeader = regexp.MustCompile(`(?m)^\[packages\][ \t]*(#.*)?$`)

// addPkg writes `name = "spec"` under [packages] in .homie.toml (like `uv add`), then installs it.
func addPkg(name, spec string) error {
	if !bareKey.MatchString(name) {
		return fmt.Errorf("package name %q: use letters, digits, - and _", name)
	}
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	if _, ok := cfg.Packages[name]; ok {
		return fmt.Errorf("%s is already in %s", name, configFile())
	}
	old, _ := os.ReadFile(configFile())
	quoted := "'" + spec + "'" // TOML literal string: no escaping needed for shell commands
	if strings.ContainsAny(spec, "'\n") {
		quoted = fmt.Sprintf("%q", spec)
	}
	line := name + " = " + quoted + "\n"
	s := string(old)
	if loc := packagesHeader.FindStringIndex(s); loc != nil {
		s = s[:loc[1]] + "\n" + strings.TrimSuffix(line, "\n") + s[loc[1]:]
	} else {
		s = strings.TrimRight(s, "\n") + "\n\n[packages]\n" + line
	}
	if err := os.WriteFile(configFile(), []byte(s), 0o644); err != nil {
		return err
	}
	if _, err := loadConfig(); err != nil { // never leave a broken config behind
		os.WriteFile(configFile(), old, 0o644)
		return err
	}
	fmt.Println("added to", configFile()+":", strings.TrimSpace(line))
	return install([]string{name})
}

// executables returns every executable regular file in the archive (a bare binary is saved as fallback).
func executables(url string, data []byte, fallback string) (map[string][]byte, error) {
	out := map[string][]byte{}
	isExe := func(name string, mode fs.FileMode) bool {
		if runtime.GOOS == "windows" {
			return strings.HasSuffix(name, ".exe")
		}
		return mode&0o111 != 0
	}
	switch {
	case strings.HasSuffix(url, ".zip"):
		zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			return nil, err
		}
		for _, f := range zr.File {
			if f.Mode().IsRegular() && isExe(f.Name, f.Mode()) {
				rc, err := f.Open()
				if err != nil {
					return nil, err
				}
				b, err := io.ReadAll(rc)
				rc.Close()
				if err != nil {
					return nil, err
				}
				out[path.Base(f.Name)] = b
			}
		}
	case strings.HasSuffix(url, ".tar.gz"), strings.HasSuffix(url, ".tgz"):
		gz, err := gzip.NewReader(bytes.NewReader(data))
		if err != nil {
			return nil, err
		}
		tr := tar.NewReader(gz)
		for {
			h, err := tr.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				return nil, err
			}
			if h.Typeflag == tar.TypeReg && isExe(h.Name, fs.FileMode(h.Mode)) {
				b, err := io.ReadAll(tr)
				if err != nil {
					return nil, err
				}
				out[path.Base(h.Name)] = b
			}
		}
	default:
		out[fallback] = data
	}
	if len(out) == 0 {
		return nil, errors.New("no executables in " + path.Base(url) + "; set bins = [...]")
	}
	return out, nil
}

// extract returns member (matched by base name) from a .zip/.tar.gz/.tgz archive, or the download itself for a bare binary.
func extract(url string, data []byte, member string) ([]byte, error) {
	switch {
	case strings.HasSuffix(url, ".zip"):
		zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			return nil, err
		}
		for _, f := range zr.File {
			if path.Base(f.Name) == member && !f.FileInfo().IsDir() {
				rc, err := f.Open()
				if err != nil {
					return nil, err
				}
				defer rc.Close()
				return io.ReadAll(rc)
			}
		}
	case strings.HasSuffix(url, ".tar.gz"), strings.HasSuffix(url, ".tgz"):
		gz, err := gzip.NewReader(bytes.NewReader(data))
		if err != nil {
			return nil, err
		}
		tr := tar.NewReader(gz)
		for {
			h, err := tr.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				return nil, err
			}
			if path.Base(h.Name) == member && h.Typeflag == tar.TypeReg {
				return io.ReadAll(tr)
			}
		}
	default:
		return data, nil
	}
	return nil, errors.New(member + " not found in " + path.Base(url))
}

// replaceFile swaps in new content atomically; on Windows a running exe can be renamed but not overwritten.
func replaceFile(dst string, data []byte, mode fs.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	tmp := dst + ".new"
	if err := os.WriteFile(tmp, data, mode); err != nil {
		return err
	}
	if runtime.GOOS == "windows" {
		os.Remove(dst + ".old")
		if err := os.Rename(dst, dst+".old"); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return os.Rename(tmp, dst)
}

func outdated() error {
	ps, err := packages()
	if err != nil {
		return err
	}
	st, n := loadState(), 0
	for _, p := range ps {
		if p.Script != "" || st[p.Name].Tag == "" {
			continue
		}
		tag, _, err := latest(p)
		if err != nil {
			return err
		}
		if tag != st[p.Name].Tag {
			fmt.Printf("%-10s %s -> %s\n", p.Name, st[p.Name].Tag, tag)
			n++
		}
	}
	if n == 0 {
		fmt.Println("all release packages are up to date (install-command packages update via `homie upgrade`)")
	}
	return nil
}

// uninstall removes only what homie put under $HOME; a copy from brew/apt elsewhere on PATH is left alone.
func uninstall(names []string) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	st := loadState()
	for _, n := range names {
		raw, ok := cfg.Packages[n]
		if _, known := st[n]; !ok && !known {
			return fmt.Errorf("%q is not in %s and was never installed by homie", n, configFile())
		} // already dropped from the config: the recorded bins are enough
		p, _ := resolve(n, raw, machine())
		bins := p.bins(st)
		if p.Script != "" {
			bins = append(bins, n)
		}
		var paths []string
		for _, b := range bins {
			paths = append(paths, filepath.Join(binDir(), exe(b)))
		}
		for _, d := range p.Files {
			paths = append(paths, filepath.Join(home(), filepath.FromSlash(d)))
		}
		for _, r := range p.Remove {
			paths = append(paths, filepath.Join(home(), filepath.FromSlash(r)))
		}
		removed := false
		for _, path := range paths {
			if _, err := os.Lstat(path); err == nil {
				if err := os.RemoveAll(path); err != nil {
					return err
				}
				fmt.Println("removed", path)
				removed = true
			}
		}
		if !removed {
			fmt.Println(n, "was not installed by homie, nothing removed")
		}
		delete(st, n)
	}
	return saveState(st)
}

func listPkgs() error {
	ps, err := packages()
	st := loadState()
	for _, p := range ps {
		mark := " "
		if installed(p) {
			mark = "✓"
		}
		spec := p.spec()
		if len(spec) > 60 {
			spec = spec[:57] + "..."
		}
		fmt.Printf("%s %-10s %-10s %s\n", mark, p.Name, st[p.Name].Tag, spec)
	}
	return err
}

var version = "dev" // set by -ldflags "-X main.version=vX.Y.Z"

func selfRepo() string {
	if r := os.Getenv("HOMIE_REPO"); r != "" {
		return r
	}
	return "ryanzhangtianran/homie"
}

func selfUpdate() error {
	me, err := os.Executable()
	if err != nil {
		return err
	}
	if me, err = filepath.EvalSymlinks(me); err != nil {
		return err
	}
	tag, url, err := latest(Pkg{URL: "https://github.com/" + selfRepo() + "/releases/latest/download/homie_{os}_{arch}{ext}"})
	if err != nil {
		return err
	}
	if tag == version {
		fmt.Println("homie", tag, "is up to date")
		return nil
	}
	data, err := fetch(url)
	if err != nil {
		return err
	}
	if err := replaceFile(me, data, 0o755); err != nil {
		return err
	}
	fmt.Println("updated homie", version, "->", tag)
	return nil
}
