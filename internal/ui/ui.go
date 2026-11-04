package ui

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
	"github.com/ryanzhangtianran/homie/internal/config"
)

func Confirm(title string) bool {
	ok := false
	return Ask(huh.NewConfirm().Title(title).Value(&ok)) == nil && ok
}

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

var ThemeNames = []string{"catppuccin", "nord", "terminal", "plain"}

var (
	Muted, cBorder, Accent, TitleColor, OK, Warn, Err, Text lipgloss.Color

	Frame, Bold, Dim lipgloss.Style
	Theme            *huh.Theme
)

func init() { usePalette(palettes["catppuccin"]) }

func themeFile() string { return filepath.Join(config.StateDir(), "theme") }

// LoadTheme applies the saved theme ($HOMIE_THEME wins); unknown names fall back to the default.
func LoadTheme() {
	name := os.Getenv("HOMIE_THEME")
	if name == "" {
		b, _ := os.ReadFile(themeFile())
		name = strings.TrimSpace(string(b))
	}
	if p, ok := palettes[name]; ok {
		usePalette(p)
	}
}

func SetTheme(name string) error {
	p, ok := palettes[name]
	if !ok {
		return fmt.Errorf("unknown theme %q (have: %s)", name, strings.Join(ThemeNames, ", "))
	}
	usePalette(p)
	if err := os.MkdirAll(config.StateDir(), 0o755); err != nil {
		return err
	}
	return os.WriteFile(themeFile(), []byte(name+"\n"), 0o644)
}

func usePalette(p palette) {
	Muted, cBorder, Accent, TitleColor, OK, Warn, Err, Text = p.Muted, p.Border, p.Accent, p.Title, p.OK, p.Warn, p.Err, p.Text
	Frame = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(cBorder).Padding(0, 1)
	Bold = lipgloss.NewStyle().Bold(true)
	Dim = lipgloss.NewStyle().Foreground(Muted)
	Theme = huhTheme()
}

// huhTheme builds the form theme from the palette: the whole form is framed, fields have no side bar.
func huhTheme() *huh.Theme {
	t := huh.ThemeBase()
	fg := func(c lipgloss.Color) lipgloss.Style { return lipgloss.NewStyle().Foreground(c) }
	t.Form.Base = Frame
	f := &t.Focused
	f.Base, f.Card = lipgloss.NewStyle(), lipgloss.NewStyle()
	f.Title = fg(TitleColor).Bold(true)
	f.Description = fg(Muted)
	f.SelectSelector = fg(Accent).SetString("› ")
	f.Option, f.UnselectedOption = fg(Text), fg(Text)
	f.SelectedOption = fg(Accent).Bold(true)
	f.MultiSelectSelector = fg(Accent).SetString("› ")
	f.SelectedPrefix = fg(OK).SetString("✓ ")
	f.UnselectedPrefix = fg(Muted).SetString("• ")
	f.ErrorIndicator, f.ErrorMessage = fg(Err).SetString(" *"), fg(Err)
	f.FocusedButton = lipgloss.NewStyle().Padding(0, 2).MarginRight(1).Foreground(lipgloss.Color("#232634")).Background(Accent)
	f.BlurredButton = lipgloss.NewStyle().Padding(0, 2).MarginRight(1).Foreground(Text).Background(cBorder)
	f.Next = f.FocusedButton
	f.TextInput.Cursor, f.TextInput.Prompt = fg(Accent), fg(Accent)
	f.TextInput.Placeholder, f.TextInput.Text = fg(Muted), fg(Text)
	t.Blurred = t.Focused
	t.Blurred.Title = fg(Muted)
	t.Blurred.SelectSelector = lipgloss.NewStyle().SetString("  ")
	t.Blurred.MultiSelectSelector = lipgloss.NewStyle().SetString("  ")
	t.Group.Title, t.Group.Description = t.Focused.Title, t.Focused.Description
	t.Help.ShortKey, t.Help.FullKey = fg(Text), fg(Text)
	t.Help.ShortDesc, t.Help.FullDesc = fg(Muted), fg(Muted)
	t.Help.ShortSeparator, t.Help.FullSeparator = fg(cBorder), fg(cBorder)
	return t
}

// Say prints one progress line: a colored icon and a fixed-width verb, so messages line up.
func Say(c lipgloss.Color, icon, verb, msg string) {
	fmt.Fprintln(Out, lipgloss.NewStyle().Foreground(c).Render(fmt.Sprintf("%s %-10s", icon, verb))+" "+msg)
}

func SayErr(err error) {
	fmt.Fprintln(os.Stderr, lipgloss.NewStyle().Foreground(Err).Render(fmt.Sprintf("✗ %-10s", "error"))+" "+err.Error())
}

// BoxWidth is every frame's width including padding (80 columns with the border); the menu form matches it.
// A box only grows when its content cannot fit.
var BoxWidth = 78

// Out is where reports (status, package lists, progress lines) go; the full-screen UI captures it.
var Out io.Writer = os.Stdout

// Box frames body under a bold title.
func Box(title, body string) string {
	t := Bold.Foreground(Accent).Render(title)
	return Frame.Width(max(lipgloss.Width(body)+2, lipgloss.Width(t)+2, BoxWidth)).Render(t + "\n\n" + body)
}

// Columns pads cells by display width so every column lines up; a header gets a rule under it.
func Columns(header []string, rows [][]string) string {
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
			h[i] = Bold.Foreground(Muted).Render(c)
		}
		total := 0
		for _, w := range widths {
			total += w + 2
		}
		out = append(out, line(h), Dim.Render(strings.Repeat("─", max(total-2, BoxWidth-2)))) // spans the whole box
	}
	for _, r := range rows {
		out = append(out, line(r))
	}
	return strings.Join(out, "\n")
}

func Grid(title string, header []string, rows [][]string) {
	fmt.Fprintln(Out, Box(title, Columns(header, rows)))
}

// Ask runs fields as one framed form.
func Ask(fields ...huh.Field) error {
	return huh.NewForm(huh.NewGroup(fields...)).WithTheme(Theme).WithWidth(BoxWidth - 2).Run()
}
