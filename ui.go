package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
)

// palette is a color scheme; `homie theme <name>` picks one and saves it in the state dir.
type palette struct {
	Muted, Border, Accent, Title, OK, Warn, Err, Text lipgloss.Color
}

var palettes = map[string]palette{
	// catppuccin frappé, the same family as the starship prompt
	"catppuccin": {Muted: "#a5adce", Border: "#737994", Accent: "#85c1dc", Title: "#ca9ee6",
		OK: "#a6d189", Warn: "#e5c890", Err: "#e78284", Text: "#c6d0f5"},
	"nord": {Muted: "#d8dee9", Border: "#616e88", Accent: "#88c0d0", Title: "#b48ead",
		OK: "#a3be8c", Warn: "#ebcb8b", Err: "#bf616a", Text: "#eceff4"},
	// the terminal's own 16 colors, bright variants so nothing disappears on dark or translucent backgrounds
	"terminal": {Muted: "7", Border: "7", Accent: "14", Title: "13", OK: "10", Warn: "11", Err: "9", Text: "15"},
	"plain":    {},
}

var themeNames = []string{"catppuccin", "nord", "terminal", "plain"}

var (
	cDim, cBorder, cAccent, cTitle, cOK, cWarn, cErr, cText lipgloss.Color

	frame, bold, dim lipgloss.Style
	theme            *huh.Theme
)

func init() { usePalette(palettes["catppuccin"]) }

func themeFile() string { return filepath.Join(stateDir(), "theme") }

// loadTheme applies the saved theme ($HOMIE_THEME wins); unknown names fall back to the default.
func loadTheme() {
	name := os.Getenv("HOMIE_THEME")
	if name == "" {
		b, _ := os.ReadFile(themeFile())
		name = strings.TrimSpace(string(b))
	}
	if p, ok := palettes[name]; ok {
		usePalette(p)
	}
}

func setTheme(name string) error {
	p, ok := palettes[name]
	if !ok {
		return fmt.Errorf("unknown theme %q (have: %s)", name, strings.Join(themeNames, ", "))
	}
	usePalette(p)
	if err := os.MkdirAll(stateDir(), 0o755); err != nil {
		return err
	}
	return os.WriteFile(themeFile(), []byte(name+"\n"), 0o644)
}

func usePalette(p palette) {
	cDim, cBorder, cAccent, cTitle, cOK, cWarn, cErr, cText = p.Muted, p.Border, p.Accent, p.Title, p.OK, p.Warn, p.Err, p.Text
	frame = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(cBorder).Padding(0, 1)
	bold = lipgloss.NewStyle().Bold(true)
	dim = lipgloss.NewStyle().Foreground(cDim)
	theme = huhTheme()
}

// huhTheme builds the form theme from the palette: the whole form is framed, fields have no side bar.
func huhTheme() *huh.Theme {
	t := huh.ThemeBase()
	fg := func(c lipgloss.Color) lipgloss.Style { return lipgloss.NewStyle().Foreground(c) }
	t.Form.Base = frame
	f := &t.Focused
	f.Base, f.Card = lipgloss.NewStyle(), lipgloss.NewStyle()
	f.Title = fg(cTitle).Bold(true)
	f.Description = fg(cDim)
	f.SelectSelector = fg(cAccent).SetString("› ")
	f.Option, f.UnselectedOption = fg(cText), fg(cText)
	f.SelectedOption = fg(cAccent).Bold(true)
	f.MultiSelectSelector = fg(cAccent).SetString("› ")
	f.SelectedPrefix = fg(cOK).SetString("✓ ")
	f.UnselectedPrefix = fg(cDim).SetString("• ")
	f.ErrorIndicator, f.ErrorMessage = fg(cErr).SetString(" *"), fg(cErr)
	f.FocusedButton = lipgloss.NewStyle().Padding(0, 2).MarginRight(1).Foreground(lipgloss.Color("#232634")).Background(cAccent)
	f.BlurredButton = lipgloss.NewStyle().Padding(0, 2).MarginRight(1).Foreground(cText).Background(cBorder)
	f.Next = f.FocusedButton
	f.TextInput.Cursor, f.TextInput.Prompt = fg(cAccent), fg(cAccent)
	f.TextInput.Placeholder, f.TextInput.Text = fg(cDim), fg(cText)
	t.Blurred = t.Focused
	t.Blurred.Title = fg(cDim)
	t.Blurred.SelectSelector = lipgloss.NewStyle().SetString("  ")
	t.Blurred.MultiSelectSelector = lipgloss.NewStyle().SetString("  ")
	t.Group.Title, t.Group.Description = t.Focused.Title, t.Focused.Description
	t.Help.ShortKey, t.Help.FullKey = fg(cText), fg(cText)
	t.Help.ShortDesc, t.Help.FullDesc = fg(cDim), fg(cDim)
	t.Help.ShortSeparator, t.Help.FullSeparator = fg(cBorder), fg(cBorder)
	return t
}

// say prints one progress line: a colored icon and a fixed-width verb, so messages line up.
func say(c lipgloss.Color, icon, verb, msg string) {
	fmt.Println(lipgloss.NewStyle().Foreground(c).Render(fmt.Sprintf("%s %-10s", icon, verb)) + " " + msg)
}

func sayErr(err error) {
	fmt.Fprintln(os.Stderr, lipgloss.NewStyle().Foreground(cErr).Render(fmt.Sprintf("✗ %-10s", "error"))+" "+err.Error())
}

// boxWidth is every frame's width including padding (80 columns with the border); the menu form matches it.
// A box only grows when its content cannot fit.
const boxWidth = 78

// box frames body under a bold title.
func box(title, body string) string {
	t := bold.Foreground(cAccent).Render(title)
	return frame.Width(max(lipgloss.Width(body)+2, lipgloss.Width(t)+2, boxWidth)).Render(t + "\n\n" + body)
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
		out = append(out, line(h), dim.Render(strings.Repeat("─", max(total-2, boxWidth-2)))) // spans the whole box
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
	return box("homie "+version, body)
}

func relHome(p string) (string, error) {
	r := rel(p)
	if strings.HasPrefix(r, "..") {
		return "", fmt.Errorf("outside home")
	}
	return r, nil
}

// ask runs fields as one framed form.
func ask(fields ...huh.Field) error {
	return huh.NewForm(huh.NewGroup(fields...)).WithTheme(theme).WithWidth(boxWidth - 2).Run()
}
