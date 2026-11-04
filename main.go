package main

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/ryanzhangtianran/homie/internal/config"
	"github.com/ryanzhangtianran/homie/internal/dotfiles"
	"github.com/ryanzhangtianran/homie/internal/pkgs"
	"github.com/ryanzhangtianran/homie/internal/ui"
)

var version = "dev"

func main() {
	ui.LoadTheme()
	if err := run(os.Args[1:]); err != nil && !errors.Is(err, huh.ErrUserAborted) { // Ctrl-C is not an error
		ui.SayErr(err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return menuLoop()
	}
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "init":
		repo := ""
		if len(rest) > 0 {
			repo = rest[0]
		}
		return dotfiles.Init(repo)
	case "remote":
		if len(rest) == 0 {
			return dotfiles.Git("remote", "-v")
		}
		return dotfiles.Bind(rest[0])
	case "push":
		msg := "update dotfiles"
		if len(rest) > 0 {
			msg = strings.Join(rest, " ")
		}
		return dotfiles.Push(msg)
	case "add":
		tag := ""
		if len(rest) > 1 && (rest[0] == "-t" || rest[0] == "--tag") {
			tag, rest = rest[1], rest[2:]
		}
		for _, p := range rest {
			if err := dotfiles.Add(p, tag); err != nil {
				return err
			}
		}
		return nil
	case "status":
		return dotfiles.Status()
	case "diff":
		return dotfiles.Diff()
	case "apply":
		return dotfiles.Apply()
	case "pull":
		if err := dotfiles.Git("pull", "--ff-only"); err != nil {
			return err
		}
		return dotfiles.ApplyWith(false)
	case "theme":
		if len(rest) == 0 {
			fmt.Println(strings.Join(ui.ThemeNames, "\n"))
			return nil
		}
		return ui.SetTheme(rest[0])
	case "update":
		if err := dotfiles.Git("pull", "--ff-only"); err != nil {
			return err
		}
		if err := dotfiles.Apply(); err != nil {
			return err
		}
		return pkgs.Install(nil)
	case "machine":
		if len(rest) == 0 {
			fmt.Println(config.Machine())
			return nil
		}
		return config.SetMachine(rest[0])
	case "pkgs":
		return pkgs.List()
	case "install", "upgrade":
		return pkgs.Install(rest)
	case "add-pkg":
		if len(rest) < 2 {
			return errors.New(`usage: homie add-pkg <name> <gh:owner/repo | "install command">`)
		}
		return pkgs.AddPkg(rest[0], strings.Join(rest[1:], " "))
	case "outdated":
		return pkgs.Outdated()
	case "uninstall", "remove":
		return pkgs.Uninstall(rest)
	case "self-update":
		return pkgs.SelfUpdate(version)
	case "cd":
		fmt.Println(config.SrcDir())
		return nil
	case "git":
		return dotfiles.Git(rest...)
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

var helpSections = []struct {
	title string
	rows  [][]string
}{
	{"Dotfiles", [][]string{
		{"homie", "interactive UI"},
		{"status | diff", "what apply would change"},
		{"apply", "write files, install missing packages"},
		{"add [-t tag] <path>...", "manage files; -t server = ##server variant"},
		{"machine [name]", "show or set this machine's name"},
		{"pull", "update dotfiles only, no packages"},
		{"update", "pull + apply + upgrade packages"},
	}},
	{"Repo", [][]string{
		{"init [user/repo[@branch]]", "clone your config repo, or create and bind it"},
		{"remote [user/repo]", "show or change the bound repo"},
		{"push [message]", "commit everything and push"},
		{"cd", `print the source dir: cd "$(homie cd)"`},
		{"git <args>", "run git in the source dir"},
		{"theme [name]", "list or set the color scheme"},
	}},
	{"Packages", [][]string{
		{"pkgs", "list this machine's packages"},
		{"add-pkg <name> <spec>", "add a package to .homie.toml and install it"},
		{"install [pkg...]", "install or upgrade; named ones always rerun"},
		{"upgrade", "upgrade all packages"},
		{"outdated", "check for new releases"},
		{"uninstall <pkg...>", "remove what homie installed"},
		{"self-update", "update homie itself"},
	}},
}

// help aligns all sections as one block so the descriptions share a column.
func help() string {
	var rows [][]string
	for i, s := range helpSections {
		if i > 0 {
			rows = append(rows, []string{""})
		}
		rows = append(rows, []string{ui.Bold.Render(s.title)})
		for _, r := range s.rows {
			rows = append(rows, []string{"  " + r[0], ui.Dim.Render(r[1])})
		}
	}
	return ui.Box("homie "+version+" · dotfiles + user-level tools, no sudo", ui.Columns(nil, rows))
}
