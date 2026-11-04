package main

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/huh"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		sayErr(err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return ui()
	}
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "init":
		repo := ""
		if len(rest) > 0 {
			repo = rest[0]
		}
		return initSource(repo)
	case "remote":
		if len(rest) == 0 {
			return git("remote", "-v")
		}
		return bind(rest[0])
	case "push":
		msg := "update dotfiles"
		if len(rest) > 0 {
			msg = strings.Join(rest, " ")
		}
		return push(msg)
	case "add":
		tag := ""
		if len(rest) > 1 && (rest[0] == "-t" || rest[0] == "--tag") {
			tag, rest = rest[1], rest[2:]
		}
		for _, p := range rest {
			if err := add(p, tag); err != nil {
				return err
			}
		}
		return nil
	case "status":
		return status()
	case "diff":
		return diff()
	case "apply":
		return apply()
	case "update":
		if err := git("pull", "--ff-only"); err != nil {
			return err
		}
		if err := apply(); err != nil {
			return err
		}
		return install(nil)
	case "machine":
		if len(rest) == 0 {
			fmt.Println(machine())
			return nil
		}
		return setMachine(rest[0])
	case "pkgs":
		return listPkgs()
	case "install", "upgrade":
		return install(rest)
	case "add-pkg":
		if len(rest) < 2 {
			return errors.New(`usage: homie add-pkg <name> <gh:owner/repo | "install command">`)
		}
		return addPkg(rest[0], strings.Join(rest[1:], " "))
	case "outdated":
		return outdated()
	case "uninstall", "remove":
		return uninstall(rest)
	case "self-update":
		return selfUpdate()
	case "cd":
		fmt.Println(srcDir())
		return nil
	case "git":
		return git(rest...)
	case "version", "--version":
		fmt.Println(version)
		return nil
	}
	fmt.Println(help())
	if cmd == "help" || cmd == "-h" || cmd == "--help" {
		return nil
	}
	return fmt.Errorf("unknown command %q", cmd)
}

func ui() error {
	if !dirExists(srcDir()) {
		repo := ""
		if err := ask(huh.NewInput().Title("No config repo yet").Description("GitHub repo to bind, e.g. you/dotfiles (empty = local only)").Value(&repo)); err != nil {
			return err
		}
		if err := initSource(repo); err != nil {
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
		err := ask(huh.NewSelect[string]().Title("What next?").Options(opts...).Value(&choice))
		if errors.Is(err, huh.ErrUserAborted) || choice == "quit" {
			return nil
		}
		if err != nil {
			return err
		}
		if err := uiAction(choice); err != nil && !errors.Is(err, huh.ErrUserAborted) {
			sayErr(err)
		}
		fmt.Println()
	}
}

// menu entries: command, label, description (labels padded so descriptions line up).
var menu = [][3]string{
	{"status", "Status", "what apply would change"},
	{"diff", "Diff", "line-by-line changes"},
	{"apply", "Apply", "write files, install missing packages"},
	{"add", "Add file", "start managing a file under $HOME"},
	{"push", "Push", "commit and push to GitHub"},
	{"install", "Install", "install or upgrade packages"},
	{"add-pkg", "Add package", "add a line to .homie.toml"},
	{"outdated", "Outdated", "check for new releases"},
	{"uninstall", "Uninstall", "remove packages"},
	{"update", "Update all", "pull + apply + upgrade"},
	{"machine", "Machine", "set this machine's name"},
	{"self-update", "Self-update", "update homie itself"},
	{"quit", "Quit", ""},
}

// pickPkgs shows a multi-select of this machine's packages, filtered by keep.
func pickPkgs(title string, keep func(Pkg) bool) ([]string, error) {
	ps, err := packages()
	if err != nil {
		return nil, err
	}
	var opts []huh.Option[string]
	for _, p := range ps {
		if !keep(p) {
			continue
		}
		label := p.Name
		if installed(p) {
			label += " ✓"
		}
		opts = append(opts, huh.NewOption(label, p.Name))
	}
	if len(opts) == 0 {
		return nil, errors.New("no packages")
	}
	var picked []string
	err = ask(huh.NewMultiSelect[string]().Title(title).Description("space to toggle, enter to confirm").Options(opts...).Value(&picked))
	return picked, err
}

func confirm(title string) bool {
	ok := false
	return ask(huh.NewConfirm().Title(title).Value(&ok)) == nil && ok
}

func uiAction(choice string) error {
	switch choice {
	case "apply":
		es, err := changed()
		if err != nil {
			return err
		}
		if len(es) > 0 {
			_ = status()
			if !confirm(fmt.Sprintf("Apply %d file(s)?", len(es))) {
				return nil
			}
		}
		return apply()
	case "add":
		p, tag := "", ""
		err := ask(
			huh.NewInput().Title("Path to add").Placeholder("~/.bashrc").Value(&p),
			huh.NewInput().Title("Only for machine/OS").Placeholder("empty = all").Value(&tag),
		)
		if err != nil || p == "" {
			return err
		}
		if strings.HasPrefix(p, "~/") {
			p = home() + p[1:]
		}
		return add(p, tag)
	case "push":
		msg := "update dotfiles"
		if err := ask(huh.NewInput().Title("Commit message").Value(&msg)); err != nil {
			return err
		}
		return push(msg)
	case "install":
		picked, err := pickPkgs("Install / upgrade", func(Pkg) bool { return true })
		if err != nil || len(picked) == 0 {
			return err
		}
		return install(picked)
	case "add-pkg":
		name, spec := "", ""
		err := ask(
			huh.NewInput().Title("Name").Placeholder("bat").Value(&name),
			huh.NewInput().Title("Spec").Description("gh:owner/repo or an install command").Placeholder("gh:sharkdp/bat").Value(&spec),
		)
		if err != nil || name == "" || spec == "" {
			return err
		}
		return addPkg(name, spec)
	case "uninstall":
		picked, err := pickPkgs("Uninstall", installed)
		if err != nil || len(picked) == 0 || !confirm("Remove "+strings.Join(picked, ", ")+"?") {
			return err
		}
		return uninstall(picked)
	case "machine":
		m := machine()
		if err := ask(huh.NewInput().Title("Machine name").Description("selects ##variants, e.g. server").Value(&m)); err != nil || m == "" {
			return err
		}
		return setMachine(m)
	}
	return run([]string{choice})
}
