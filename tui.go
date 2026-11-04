package main

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
	"github.com/ryanzhangtianran/homie/internal/config"
	"github.com/ryanzhangtianran/homie/internal/dotfiles"
	"github.com/ryanzhangtianran/homie/internal/pkgs"
	"github.com/ryanzhangtianran/homie/internal/ui"
)

func menuLoop() error {
	if !config.Exists(config.SrcDir()) {
		repo := ""
		if err := ui.Ask(huh.NewInput().Title("No config repo yet").Description("GitHub repo to bind, e.g. you/dotfiles (empty = local only)").Value(&repo)); err != nil {
			return err
		}
		if err := dotfiles.Init(repo); err != nil {
			return err
		}
	}
	for {
		fmt.Println(header())
		var opts []huh.Option[string]
		for _, m := range menu {
			opts = append(opts, huh.NewOption(fmt.Sprintf("%-12s %s", m[1], m[2]), m[0]))
		}
		var choice string
		err := ui.Ask(huh.NewSelect[string]().Title("What next?").Options(opts...).Value(&choice))
		if errors.Is(err, huh.ErrUserAborted) || choice == "quit" {
			return nil
		}
		if err != nil {
			return err
		}
		if err := uiAction(choice); err != nil && !errors.Is(err, huh.ErrUserAborted) {
			ui.SayErr(err)
		}
		fmt.Println()
	}
}

// menu entries: command, label, description, alphabetical by label with Quit last
// (labels are padded when rendered so the descriptions line up).
var menu = [][3]string{
	{"add", "Add file", "start managing a file under $HOME"},
	{"add-pkg", "Add package", "add a line to .homie.toml"},
	{"apply", "Apply", "write files, install missing packages"},
	{"diff", "Diff", "line-by-line changes"},
	{"install", "Install", "install or upgrade packages"},
	{"machine", "Machine", "set this machine's name"},
	{"outdated", "Outdated", "check for new releases"},
	{"pull", "Pull", "update dotfiles only (git pull + write files)"},
	{"push", "Push", "commit and push to GitHub"},
	{"self-update", "Self-update", "update homie itself"},
	{"status", "Status", "what apply would change"},
	{"theme", "Theme", "change the color scheme"},
	{"uninstall", "Uninstall", "remove packages"},
	{"update", "Update all", "pull + apply + upgrade packages"},
	{"quit", "Quit", ""},
}

// pickPkgs shows a multi-select of this machine's packages, filtered by keep.
func pickPkgs(title string, keep func(config.Pkg) bool) ([]string, error) {
	ps, err := pkgs.Packages()
	if err != nil {
		return nil, err
	}
	var opts []huh.Option[string]
	for _, p := range ps {
		if !keep(p) {
			continue
		}
		label := p.Name
		if pkgs.Installed(p) {
			label += " ✓"
		}
		opts = append(opts, huh.NewOption(label, p.Name))
	}
	if len(opts) == 0 {
		return nil, errors.New("no packages")
	}
	var picked []string
	err = ui.Ask(huh.NewMultiSelect[string]().Title(title).Description("space to toggle, enter to confirm").Options(opts...).Value(&picked))
	return picked, err
}

func uiAction(choice string) error {
	switch choice {
	case "apply":
		es, err := dotfiles.Changed()
		if err != nil {
			return err
		}
		if len(es) > 0 {
			_ = dotfiles.Status()
			if !ui.Confirm(fmt.Sprintf("Apply %d file(s)?", len(es))) {
				return nil
			}
		}
		return dotfiles.Apply()
	case "add":
		p, tag := "", ""
		err := ui.Ask(
			huh.NewInput().Title("Path to add").Placeholder("~/.bashrc").Value(&p),
			huh.NewInput().Title("Only for machine/OS").Placeholder("empty = all").Value(&tag),
		)
		if err != nil || p == "" {
			return err
		}
		if strings.HasPrefix(p, "~/") {
			p = config.Home() + p[1:]
		}
		return dotfiles.Add(p, tag)
	case "push":
		msg := "update dotfiles"
		if err := ui.Ask(huh.NewInput().Title("Commit message").Value(&msg)); err != nil {
			return err
		}
		return dotfiles.Push(msg)
	case "install":
		picked, err := pickPkgs("Install / upgrade", func(config.Pkg) bool { return true })
		if err != nil || len(picked) == 0 {
			return err
		}
		return pkgs.Install(picked)
	case "add-pkg":
		name, spec := "", ""
		err := ui.Ask(
			huh.NewInput().Title("Name").Placeholder("bat").Value(&name),
			huh.NewInput().Title("Spec").Description("gh:owner/repo or an install command").Placeholder("gh:sharkdp/bat").Value(&spec),
		)
		if err != nil || name == "" || spec == "" {
			return err
		}
		return pkgs.AddPkg(name, spec)
	case "uninstall":
		picked, err := pickPkgs("Uninstall", pkgs.Installed)
		if err != nil || len(picked) == 0 || !ui.Confirm("Remove "+strings.Join(picked, ", ")+"?") {
			return err
		}
		return pkgs.Uninstall(picked)
	case "theme":
		var opts []huh.Option[string]
		for _, n := range ui.ThemeNames {
			opts = append(opts, huh.NewOption(n, n))
		}
		name := ""
		if err := ui.Ask(huh.NewSelect[string]().Title("Color scheme").Options(opts...).Value(&name)); err != nil {
			return err
		}
		return ui.SetTheme(name)
	case "machine":
		m := config.Machine()
		if err := ui.Ask(huh.NewInput().Title("Machine name").Description("selects ##variants, e.g. server").Value(&m)); err != nil || m == "" {
			return err
		}
		return config.SetMachine(m)
	}
	return run([]string{choice})
}

// header is the summary box above the interactive menu.
func header() string {
	remote := "not bound"
	if out, err := exec.Command("git", "-C", config.SrcDir(), "remote", "get-url", "origin").Output(); err == nil {
		remote = strings.TrimSpace(string(out))
	}
	files := ui.Dim.Render("up to date")
	if es, err := dotfiles.Changed(); err != nil {
		files = lipgloss.NewStyle().Foreground(ui.Err).Render(err.Error())
	} else if len(es) > 0 {
		files = lipgloss.NewStyle().Foreground(ui.Warn).Render(fmt.Sprintf("%d to apply", len(es)))
	}
	ps, _ := pkgs.Packages()
	n := 0
	for _, p := range ps {
		if pkgs.Installed(p) {
			n++
		}
	}
	pk := fmt.Sprintf("%d/%d installed", n, len(ps))
	if n < len(ps) {
		pk = lipgloss.NewStyle().Foreground(ui.Warn).Render(pk)
	}
	src := config.SrcDir()
	if r, err := relHome(src); err == nil {
		src = "~/" + r
	}
	body := ui.Columns(nil, [][]string{
		{ui.Dim.Render("machine"), config.Machine()},
		{ui.Dim.Render("source"), src},
		{ui.Dim.Render("remote"), remote},
		{ui.Dim.Render("files"), files},
		{ui.Dim.Render("packages"), pk},
	})
	return ui.Box("homie "+version, body)
}

func relHome(p string) (string, error) {
	r := dotfiles.Rel(p)
	if strings.HasPrefix(r, "..") {
		return "", fmt.Errorf("outside home")
	}
	return r, nil
}
