package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
)

var (
	cDim    = lipgloss.Color("8")
	cAccent = lipgloss.Color("6")
	cOK     = lipgloss.Color("2")
	cWarn   = lipgloss.Color("3")
	cErr    = lipgloss.Color("1")

	frame = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(cDim).Padding(0, 1)
	bold  = lipgloss.NewStyle().Bold(true)
	dim   = lipgloss.NewStyle().Foreground(cDim)
)

// say prints one progress line: a colored icon and a fixed-width verb, so messages line up.
func say(c lipgloss.Color, icon, verb, msg string) {
	fmt.Println(lipgloss.NewStyle().Foreground(c).Render(fmt.Sprintf("%s %-10s", icon, verb)) + " " + msg)
}

func sayErr(err error) {
	fmt.Fprintln(os.Stderr, lipgloss.NewStyle().Foreground(cErr).Render(fmt.Sprintf("✗ %-10s", "error"))+" "+err.Error())
}

// box frames body under a bold title.
func box(title, body string) string {
	return frame.Render(bold.Foreground(cAccent).Render(title) + "\n\n" + body)
}

// columns pads cells by display width so every column lines up; a header gets a rule under it.
func columns(header []string, rows [][]string) string {
	all := rows
	if len(header) > 0 {
		all = append([][]string{header}, rows...)
	}
	var widths []int
	for _, r := range all {
		for i, c := range r {
			if i == len(widths) {
				widths = append(widths, 0)
			}
			widths[i] = max(widths[i], lipgloss.Width(c))
		}
	}
	line := func(r []string) string {
		var b strings.Builder
		for i, c := range r {
			b.WriteString(c)
			if i < len(r)-1 {
				b.WriteString(strings.Repeat(" ", widths[i]-lipgloss.Width(c)+2))
			}
		}
		return strings.TrimRight(b.String(), " ")
	}
	var out []string
	if len(header) > 0 {
		h := make([]string, len(header))
		for i, c := range header {
			h[i] = bold.Foreground(cDim).Render(c)
		}
		total := 0
		for _, w := range widths {
			total += w + 2
		}
		out = append(out, line(h), dim.Render(strings.Repeat("─", total-2)))
	}
	for _, r := range rows {
		out = append(out, line(r))
	}
	return strings.Join(out, "\n")
}

func grid(title string, header []string, rows [][]string) {
	fmt.Println(box(title, columns(header, rows)))
}

var helpSections = []struct {
	title string
	rows  [][]string
}{
	{"Dotfiles", [][]string{
		{"homie", "interactive UI"},
		{"status | diff", "what apply would change"},
		{"apply", "write files (backups kept), install missing packages"},
		{"add [-t tag] <path>...", "manage files; -t server = ##server variant"},
		{"machine [name]", "show or set this machine's name"},
		{"update", "pull + apply + upgrade"},
	}},
	{"Repo", [][]string{
		{"init [user/repo[@branch]]", "clone your config repo, or create and bind it"},
		{"remote [user/repo]", "show or change the bound repo"},
		{"push [message]", "commit everything and push"},
		{"cd", `print the source dir: cd "$(homie cd)"`},
		{"git <args>", "run git in the source dir"},
	}},
	{"Packages", [][]string{
		{"pkgs", "list this machine's packages"},
		{"add-pkg <name> <spec>", "add to .homie.toml and install (gh:owner/repo or a command)"},
		{"install [pkg...]", "install or upgrade (no args: all)"},
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
		rows = append(rows, []string{bold.Render(s.title)})
		for _, r := range s.rows {
			rows = append(rows, []string{"  " + r[0], dim.Render(r[1])})
		}
	}
	return box("homie "+version+" · dotfiles + user-level tools, no sudo", columns(nil, rows))
}

// header is the summary box above the interactive menu.
func header() string {
	remote := "not bound"
	if out, err := exec.Command("git", "-C", srcDir(), "remote", "get-url", "origin").Output(); err == nil {
		remote = strings.TrimSpace(string(out))
	}
	files := dim.Render("up to date")
	if es, err := changed(); err != nil {
		files = lipgloss.NewStyle().Foreground(cErr).Render(err.Error())
	} else if len(es) > 0 {
		files = lipgloss.NewStyle().Foreground(cWarn).Render(fmt.Sprintf("%d to apply", len(es)))
	}
	ps, _ := packages()
	n := 0
	for _, p := range ps {
		if installed(p) {
			n++
		}
	}
	pk := fmt.Sprintf("%d/%d installed", n, len(ps))
	if n < len(ps) {
		pk = lipgloss.NewStyle().Foreground(cWarn).Render(pk)
	}
	src := srcDir()
	if r, err := relHome(src); err == nil {
		src = "~/" + r
	}
	body := columns(nil, [][]string{
		{dim.Render("machine"), machine()},
		{dim.Render("source"), src},
		{dim.Render("remote"), remote},
		{dim.Render("files"), files},
		{dim.Render("packages"), pk},
	})
	// at least as wide as the menu form below it (form width 64 + padding)
	return frame.Width(max(lipgloss.Width(body)+2, 66)).Render(bold.Foreground(cAccent).Render("homie "+version) + "\n\n" + body)
}

func relHome(p string) (string, error) {
	r := rel(p)
	if strings.HasPrefix(r, "..") {
		return "", fmt.Errorf("outside home")
	}
	return r, nil
}

// theme frames the whole form (fields + key help); a border on the field itself gets its right edge cut off.
var theme = func() *huh.Theme {
	t := huh.ThemeCharm()
	t.Form.Base = frame
	return t
}()

// ask runs fields as one framed form.
func ask(fields ...huh.Field) error {
	return huh.NewForm(huh.NewGroup(fields...)).WithTheme(theme).WithWidth(64).Run()
}
