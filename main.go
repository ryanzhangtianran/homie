package main

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/huh"
)

const usage = `homie - dotfiles + user-level tool manager (no sudo)

  homie                         interactive UI
  homie init [user/repo[@br]]   clone your GitHub config repo (optionally a branch), or create + bind it;
                                also takes a git URL
  homie remote [user/repo]      show or change the bound repo
  homie push [message]          commit everything in the source dir and push to the bound repo
  homie add [-t tag] <path>...  manage files under $HOME; -t server stores them as the ##server variant
  homie status | diff           show what apply would change
  homie apply                   write files into $HOME (overwritten ones are backed up), install
                                missing packages, run run_ scripts
  homie update                  git pull + apply + upgrade packages
  homie machine [name]          show or set this machine's name (selects ##variants)
  homie pkgs                    list packages from .homie.toml for this machine
  homie add-pkg <name> <spec>   add a package line and install it; spec is "gh:owner/repo"
                                or an install command, e.g. homie add-pkg bat gh:sharkdp/bat
  homie install [pkg...]        install or upgrade packages (no args: all)
  homie upgrade                 same as install with no args
  homie outdated                check which packages have new releases
  homie uninstall <pkg...>      remove packages homie installed
  homie self-update             update homie itself
  homie cd                      print the source dir (cd "$(homie cd)")
  homie git <args>              run git in the source dir`

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
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
	fmt.Println(usage)
	if cmd == "help" || cmd == "-h" || cmd == "--help" {
		return nil
	}
	return fmt.Errorf("unknown command %q", cmd)
}

func ui() error {
	if !dirExists(srcDir()) {
		repo := ""
		if err := huh.NewInput().Title("No config repo yet. GitHub repo to bind, e.g. you/dotfiles (empty = local only):").Value(&repo).Run(); err != nil {
			return err
		}
		if err := initSource(repo); err != nil {
			return err
		}
	}
	for {
		var choice string
		err := huh.NewSelect[string]().Title(fmt.Sprintf("homie %s · machine: %s", version, machine())).Options(
			huh.NewOption("Status", "status"),
			huh.NewOption("Diff", "diff"),
			huh.NewOption("Apply", "apply"),
			huh.NewOption("Add a file", "add"),
			huh.NewOption("Push changes to GitHub", "push"),
			huh.NewOption("Install / upgrade packages", "install"),
			huh.NewOption("Add a package", "add-pkg"),
			huh.NewOption("Check for package updates", "outdated"),
			huh.NewOption("Uninstall packages", "uninstall"),
			huh.NewOption("Update everything (pull + apply + upgrade)", "update"),
			huh.NewOption("Set machine name", "machine"),
			huh.NewOption("Self-update homie", "self-update"),
			huh.NewOption("Quit", "quit"),
		).Value(&choice).Run()
		if errors.Is(err, huh.ErrUserAborted) || choice == "quit" {
			return nil
		}
		if err != nil {
			return err
		}
		if err := uiAction(choice); err != nil && !errors.Is(err, huh.ErrUserAborted) {
			fmt.Fprintln(os.Stderr, "error:", err)
		}
		fmt.Println()
	}
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
	err = huh.NewMultiSelect[string]().Title(title + " (space to toggle, enter to confirm)").Options(opts...).Value(&picked).Run()
	return picked, err
}

func confirm(title string) bool {
	ok := false
	return huh.NewConfirm().Title(title).Value(&ok).Run() == nil && ok
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
		err := huh.NewForm(huh.NewGroup(
			huh.NewInput().Title("Path to add (e.g. ~/.zshrc):").Value(&p),
			huh.NewInput().Title("Only for machine/OS (empty = all):").Value(&tag),
		)).Run()
		if err != nil || p == "" {
			return err
		}
		if strings.HasPrefix(p, "~/") {
			p = home() + p[1:]
		}
		return add(p, tag)
	case "push":
		msg := "update dotfiles"
		if err := huh.NewInput().Title("Commit message:").Value(&msg).Run(); err != nil {
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
		err := huh.NewForm(huh.NewGroup(
			huh.NewInput().Title("Name:").Value(&name),
			huh.NewInput().Title("gh:owner/repo or install command:").Value(&spec),
		)).Run()
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
		if err := huh.NewInput().Title("Machine name (selects ##variants, e.g. mac / server):").Value(&m).Run(); err != nil || m == "" {
			return err
		}
		return setMachine(m)
	}
	return run([]string{choice})
}
