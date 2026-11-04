package pkgs

import (
	"encoding/json"
	"fmt"
	"io"
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

	"github.com/charmbracelet/lipgloss"
	"github.com/ryanzhangtianran/homie/internal/config"
	"github.com/ryanzhangtianran/homie/internal/ui"
)

// Packages returns the Packages that apply to this machine, sorted by name.
func Packages() ([]config.Pkg, error) {
	cfg, err := config.Load()
	m := config.Machine()
	var out []config.Pkg
	for name, p := range cfg.Packages {
		if r, ok := config.Resolve(name, p, m); ok {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, err
}

func findPkgs(names []string) ([]config.Pkg, error) {
	all, err := Packages()
	if err != nil || len(names) == 0 {
		return all, err
	}
	var out []config.Pkg
	for _, n := range names {
		i := slices.IndexFunc(all, func(p config.Pkg) bool { return p.Name == n })
		if i < 0 {
			return nil, fmt.Errorf("%q is not in %s for this machine", n, config.File())
		}
		out = append(out, all[i])
	}
	return out, nil
}

func binDir() string { return filepath.Join(config.Home(), ".local", "bin") }

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
	data, _ := os.ReadFile(filepath.Join(config.StateDir(), "packages.json"))
	_ = json.Unmarshal(data, &m)
	return m
}

func saveState(m map[string]pkgState) error {
	data, _ := json.MarshalIndent(m, "", "  ")
	if err := os.MkdirAll(config.StateDir(), 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(config.StateDir(), "packages.json"), data, 0o644)
}

// ownBins lists the executables p owns in ~/.local/bin: configured ones, else what the last install put there.
func ownBins(p config.Pkg, st map[string]pkgState) []string {
	if len(p.Bins) > 0 {
		return p.Bins
	}
	return st[p.Name].Bins
}

func Installed(p config.Pkg) bool {
	if p.Script != "" { // installed by the vendor's script, wherever it puts things
		_, err := exec.LookPath(p.Name)
		return err == nil || config.Exists(filepath.Join(binDir(), exe(p.Name)))
	}
	bins := ownBins(p, loadState())
	for _, b := range bins {
		if !config.Exists(filepath.Join(binDir(), exe(b))) {
			return false
		}
	}
	for _, dst := range p.Files {
		if !config.Exists(filepath.Join(config.Home(), dst)) {
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
func latest(p config.Pkg) (tag, url string, err error) {
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
	ui.Say(ui.Muted, "↓", "download", url)
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

// installOne installs p, or upgrades it when a newer release exists. Install commands are rerun.
func installOne(p config.Pkg) error {
	if p.Script != "" {
		ui.Say(ui.Accent, "▶", "install", p.Name+ui.Dim.Render("  (install command)"))
		c := config.Shell(p.Script)
		c.Env = append(os.Environ(), "PATH="+binDir()+string(os.PathListSeparator)+os.Getenv("PATH"))
		c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
		return c.Run()
	}
	st := loadState()
	tag, url, err := latest(p)
	if err != nil {
		return err
	}
	if tag != "" && tag == st[p.Name].Tag && Installed(p) {
		ui.Say(ui.Muted, "·", "up to date", fmt.Sprintf("%-10s %s", p.Name, tag))
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
		if err := replaceFile(filepath.Join(config.Home(), filepath.FromSlash(dst)), b, 0o644); err != nil {
			return err
		}
	}
	sort.Strings(names)
	ui.Say(ui.OK, "✓", "installed", fmt.Sprintf("%-10s %-10s %s", p.Name, tag, ui.Dim.Render(strings.Join(names, " "))))
	st[p.Name] = pkgState{Tag: tag, Bins: names}
	return saveState(st)
}

func Install(names []string) error {
	ps, err := findPkgs(names)
	if err != nil {
		return err
	}
	for _, p := range ps {
		// install commands (claude, codex, ...) redownload everything and those tools update themselves,
		// so a bulk upgrade only runs them when missing; naming the package reruns it
		if len(names) == 0 && p.Script != "" && Installed(p) {
			ui.Say(ui.Muted, "·", "installed", fmt.Sprintf("%-10s %s", p.Name, ui.Dim.Render("rerun with: homie install "+p.Name)))
			continue
		}
		if err := installOne(p); err != nil {
			return fmt.Errorf("%s: %w", p.Name, err)
		}
	}
	if !slices.Contains(filepath.SplitList(os.Getenv("PATH")), binDir()) {
		ui.Say(ui.Warn, "!", "note", "add "+binDir()+" to your PATH")
	}
	return nil
}

// InstallMissing is the apply step: only packages not yet present, so apply stays offline when nothing is missing.
func InstallMissing() error {
	ps, err := Packages()
	if err != nil {
		return err
	}
	var missing []string
	for _, p := range ps {
		if !Installed(p) {
			missing = append(missing, p.Name)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	return Install(missing)
}

var bareKey = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

var packagesHeader = regexp.MustCompile(`(?m)^\[packages\][ \t]*(#.*)?$`)

// AddPkg writes `name = "spec"` under [packages] in .homie.toml (like `uv add`), then installs it.
func AddPkg(name, spec string) error {
	if !bareKey.MatchString(name) {
		return fmt.Errorf("package name %q: use letters, digits, - and _", name)
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if _, ok := cfg.Packages[name]; ok {
		return fmt.Errorf("%s is already in %s", name, config.File())
	}
	old, _ := os.ReadFile(config.File())
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
	if err := os.WriteFile(config.File(), []byte(s), 0o644); err != nil {
		return err
	}
	if _, err := config.Load(); err != nil { // never leave a broken config behind
		os.WriteFile(config.File(), old, 0o644)
		return err
	}
	ui.Say(ui.OK, "+", "added", strings.TrimSpace(line)+ui.Dim.Render("  in "+config.File()))
	return Install([]string{name})
}

func Outdated() error {
	ps, err := Packages()
	if err != nil {
		return err
	}
	st := loadState()
	var rows [][]string
	for _, p := range ps {
		if p.Script != "" || st[p.Name].Tag == "" {
			continue
		}
		tag, _, err := latest(p)
		if err != nil {
			return err
		}
		if tag != st[p.Name].Tag {
			rows = append(rows, []string{p.Name, st[p.Name].Tag, lipgloss.NewStyle().Foreground(ui.OK).Render(tag)})
		}
	}
	if len(rows) == 0 {
		fmt.Println(ui.Box("Outdated", lipgloss.NewStyle().Foreground(ui.OK).Render("✓ all release packages are up to date")+
			"\n"+ui.Dim.Render("install-command packages update themselves, or: homie install <name>")))
		return nil
	}
	ui.Grid("Outdated", []string{"PACKAGE", "INSTALLED", "LATEST"}, rows)
	return nil
}

// Uninstall removes only what homie put under $HOME; a copy from brew/apt elsewhere on PATH is left alone.
func Uninstall(names []string) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	st := loadState()
	for _, n := range names {
		raw, ok := cfg.Packages[n]
		if _, known := st[n]; !ok && !known {
			return fmt.Errorf("%q is not in %s and was never installed by homie", n, config.File())
		} // already dropped from the config: the recorded bins are enough
		p, _ := config.Resolve(n, raw, config.Machine())
		bins := ownBins(p, st)
		if p.Script != "" {
			bins = append(bins, n)
		}
		var paths []string
		for _, b := range bins {
			paths = append(paths, filepath.Join(binDir(), exe(b)))
		}
		for _, d := range p.Files {
			paths = append(paths, filepath.Join(config.Home(), filepath.FromSlash(d)))
		}
		for _, r := range p.Remove {
			paths = append(paths, filepath.Join(config.Home(), filepath.FromSlash(r)))
		}
		removed := false
		for _, path := range paths {
			if _, err := os.Lstat(path); err == nil {
				if err := os.RemoveAll(path); err != nil {
					return err
				}
				ui.Say(ui.OK, "✓", "removed", path)
				removed = true
			}
		}
		if !removed {
			ui.Say(ui.Muted, "·", "skip", n+" was not installed by homie, nothing removed")
		}
		delete(st, n)
	}
	return saveState(st)
}

func List() error {
	ps, err := Packages()
	if err != nil {
		return err
	}
	st := loadState()
	var rows [][]string
	for _, p := range ps {
		mark := ui.Dim.Render("·")
		if Installed(p) {
			mark = lipgloss.NewStyle().Foreground(ui.OK).Render("✓")
		}
		spec := p.Spec()
		if len(spec) > 48 { // keeps the table inside the standard box width
			spec = spec[:47] + "…"
		}
		rows = append(rows, []string{mark, p.Name, st[p.Name].Tag, ui.Dim.Render(spec)})
	}
	ui.Grid("Packages · machine "+config.Machine(), []string{"", "PACKAGE", "VERSION", "SOURCE"}, rows)
	return nil
}

func selfRepo() string {
	if r := os.Getenv("HOMIE_REPO"); r != "" {
		return r
	}
	return "ryanzhangtianran/homie"
}

func SelfUpdate(current string) error {
	me, err := os.Executable()
	if err != nil {
		return err
	}
	if me, err = filepath.EvalSymlinks(me); err != nil {
		return err
	}
	tag, url, err := latest(config.Pkg{URL: "https://github.com/" + selfRepo() + "/releases/latest/download/homie_{os}_{arch}{ext}"})
	if err != nil {
		return err
	}
	if tag == current {
		ui.Say(ui.Muted, "·", "up to date", "homie "+tag)
		return nil
	}
	data, err := fetch(url)
	if err != nil {
		return err
	}
	if err := replaceFile(me, data, 0o755); err != nil {
		return err
	}
	ui.Say(ui.OK, "✓", "updated", "homie "+current+" → "+tag)
	return nil
}
