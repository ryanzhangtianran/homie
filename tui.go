package main

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
	"github.com/ryanzhangtianran/homie/internal/config"
	"github.com/ryanzhangtianran/homie/internal/dotfiles"
	"github.com/ryanzhangtianran/homie/internal/pkgs"
	"github.com/ryanzhangtianran/homie/internal/ui"
)

// menu entries: command, label, description, alphabetical by label with Quit last.
var menu = [][3]string{
	{"add", "Add file", "start managing a file under $HOME"},
	{"add-pkg", "Add package", "add a line to .homie.toml and install it"},
	{"apply", "Apply", "write files, install missing packages"},
	{"diff", "Diff", "line-by-line changes apply would make"},
	{"help", "Help", "every command, for scripts and the shell"},
	{"install", "Install", "install or upgrade packages"},
	{"machine", "Machine", "set this machine's name"},
	{"outdated", "Outdated", "check for new releases"},
	{"pkgs", "Packages", "this machine's packages"},
	{"pull", "Pull", "update dotfiles only (git pull + write files)"},
	{"push", "Push", "commit and push to GitHub"},
	{"self-update", "Self-update", "update homie itself"},
	{"status", "Status", "what apply would change"},
	{"theme", "Theme", "change the color scheme"},
	{"uninstall", "Uninstall", "remove packages"},
	{"update", "Update all", "pull + apply + upgrade packages"},
	{"quit", "Quit", ""},
}

const (
	menuWidth = 19 // longest label + selector + frame
	wideMin   = 90 // below this the menu and the content take turns on screen
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
	// no mouse mode: it would stop the terminal from selecting text to copy; pgup/pgdn scroll instead
	_, err := tea.NewProgram(newModel(), tea.WithAltScreen()).Run()
	return err
}

type model struct {
	w, h    int
	cursor  int
	content bool   // narrow layout: the content pane is showing instead of the menu
	title   string // content pane title
	view    string // what produced the content, re-rendered on resize
	vp      viewport.Model
	form    *huh.Form
	submit  func() tea.Cmd
	head    string
	busy    bool
	drawn   int // width the current report was drawn at
}

type (
	contentMsg struct {
		title, view, body string
		width             int
	}
	resizedMsg struct{ width int } // the window stopped changing width for a moment
	doneMsg    struct {
		title string
		err   error
	}
)

func newModel() model {
	return model{head: summary(), vp: viewport.New(0, 0), view: "status", title: "Status"}
}

func (m model) Init() tea.Cmd { return nil } // the first WindowSizeMsg draws Status at the real width

func (m model) wide() bool { return m.w >= wideMin }

func (m model) contentWidth() int {
	if m.wide() {
		return m.w - menuWidth - 1
	}
	return m.w
}

func (m model) bodyHeight() int {
	return max(m.h-lipgloss.Height(m.header())-2, 3) // header, then body, hint line and key line
}

// reportWidth is the box width reports are drawn at so they fill the content pane.
func (m model) reportWidth() int { return max(m.contentWidth()-2, 40) }

// layout sizes everything from the window.
func (m *model) layout() {
	m.vp.Width, m.vp.Height = m.contentWidth(), m.bodyHeight()-1 // one line for the pane title
	if m.form != nil {
		m.form = m.form.WithWidth(max(m.contentWidth()-4, 30))
	}
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		first := m.w == 0
		m.w, m.h = msg.Width, msg.Height
		m.layout()
		if first {
			return m, m.rerun()
		}
		// a drag sends dozens of these: redraw the report once the width has settled
		w := m.w
		return m, tea.Tick(150*time.Millisecond, func(time.Time) tea.Msg { return resizedMsg{w} })
	case resizedMsg:
		if msg.width == m.w && m.reportWidth() != m.drawn && m.view != "outdated" && m.form == nil && !m.busy {
			return m, m.rerun()
		}
		return m, nil
	case contentMsg:
		m.head = summary() // whatever just ran may have changed files, packages or the theme
		m.busy, m.title, m.view, m.drawn = false, msg.title, msg.view, msg.width
		m.vp.SetContent(msg.body)
		m.vp.GotoTop()
		return m, nil
	case doneMsg:
		m.head = summary()
		body := lipgloss.NewStyle().Foreground(ui.OK).Render("✓ " + msg.title + " finished")
		if msg.err != nil && !errors.Is(msg.err, huh.ErrUserAborted) {
			body = lipgloss.NewStyle().Foreground(ui.Err).Render("✗ " + msg.err.Error())
		}
		return m, show(msg.title, body+"\n\n"+ui.Dim.Render("its output was shown in the terminal"))
	}
	if m.form != nil {
		return m.updateForm(msg)
	}
	if k, ok := msg.(tea.KeyMsg); ok {
		switch k.String() {
		case "ctrl+c", "q":
			return m, tea.Quit
		case "up", "k":
			if !m.content || m.wide() {
				m.cursor = (m.cursor + len(menu) - 1) % len(menu)
				return m, nil
			}
		case "down", "j":
			if !m.content || m.wide() {
				m.cursor = (m.cursor + 1) % len(menu)
				return m, nil
			}
		case "esc", "left", "h":
			m.content = false
			return m, nil
		case "enter", "right", "l":
			if m.busy {
				return m, nil
			}
			m.content = true
			cmd := m.choose(menu[m.cursor][0])
			return m, cmd
		}
	}
	var cmd tea.Cmd
	m.vp, cmd = m.vp.Update(msg) // scrolling: pgup/pgdn, mouse wheel, and arrows in the narrow content pane
	return m, cmd
}

func (m model) updateForm(msg tea.Msg) (tea.Model, tea.Cmd) {
	if k, ok := msg.(tea.KeyMsg); ok && k.String() == "esc" {
		m.form, m.content = nil, false
		return m, nil
	}
	f, cmd := m.form.Update(msg)
	m.form = f.(*huh.Form)
	switch m.form.State {
	case huh.StateCompleted:
		submit := m.submit
		m.form = nil
		return m, tea.Batch(cmd, submit())
	case huh.StateAborted:
		m.form, m.content = nil, false
	}
	return m, cmd
}

// openForm shows fields in the content pane; submit runs when the form is completed.
// submit must not touch m: Update works on copies, so results travel back as messages.
func (m *model) openForm(title string, submit func() tea.Cmd, fields ...huh.Field) tea.Cmd {
	m.title, m.view, m.submit = title, "", submit
	m.form = huh.NewForm(huh.NewGroup(fields...)).WithTheme(ui.Theme).WithShowHelp(true)
	m.layout()
	return m.form.Init()
}

func (m *model) rerun() tea.Cmd {
	return m.choose(m.view)
}

// choose runs a menu entry: reports render into the content pane, forms open there, and anything that
// runs other programs (git, installers) gets the real terminal for a moment.
func (m *model) choose(id string) tea.Cmd {
	label, w := id, m.reportWidth()
	for _, e := range menu {
		if e[0] == id {
			label = e[1]
		}
	}
	switch id {
	case "quit":
		return tea.Quit
	case "status":
		return capture("Status", id, w, dotfiles.Status)
	case "diff":
		return capture("Diff", id, w, dotfiles.Diff)
	case "pkgs":
		return capture("Packages", id, w, pkgs.List)
	case "help":
		return capture("Help", id, w, func() error { _, err := fmt.Fprintln(ui.Out, help()); return err })
	case "outdated":
		m.busy = true
		m.title = "Outdated"
		m.vp.SetContent(ui.Dim.Render("checking GitHub for new releases…"))
		return capture("Outdated", id, w, pkgs.Outdated)
	case "pull", "update", "self-update":
		return runTerm(label, func() error { return run([]string{id}) })
	case "apply":
		es, err := dotfiles.Changed()
		if err != nil || len(es) == 0 {
			return runTerm("Apply", dotfiles.Apply)
		}
		ok := false
		return m.openForm("Apply", func() tea.Cmd {
			if !ok {
				return show("Apply", ui.Dim.Render("nothing changed"))
			}
			return runTerm("Apply", dotfiles.Apply)
		}, huh.NewConfirm().Title(fmt.Sprintf("Apply %d file(s)?", len(es))).Description("Status lists them").Value(&ok))
	case "add":
		p, tag := "", ""
		return m.openForm("Add file", func() tea.Cmd {
			if strings.HasPrefix(p, "~/") {
				p = config.Home() + p[1:]
			}
			return capture("Add file", "", w, func() error { return dotfiles.Add(p, tag) })
		},
			huh.NewInput().Title("Path to add").Placeholder("~/.bashrc").Value(&p),
			huh.NewInput().Title("Only for machine/OS").Placeholder("empty = all").Value(&tag))
	case "add-pkg":
		name, spec := "", ""
		return m.openForm("Add package", func() tea.Cmd {
			return runTerm("Add package", func() error { return pkgs.AddPkg(name, spec) })
		},
			huh.NewInput().Title("Name").Placeholder("bat").Value(&name),
			huh.NewInput().Title("Spec").Description("gh:owner/repo or an install command").Placeholder("gh:sharkdp/bat").Value(&spec))
	case "push":
		msg := "update dotfiles"
		return m.openForm("Push", func() tea.Cmd { return runTerm("Push", func() error { return dotfiles.Push(msg) }) },
			huh.NewInput().Title("Commit message").Value(&msg))
	case "install", "uninstall":
		opts, err := pkgOptions(id == "uninstall")
		if err != nil {
			return show(label, ui.Dim.Render(err.Error()))
		}
		var picked []string
		return m.openForm(label, func() tea.Cmd {
			if len(picked) == 0 {
				return show(label, ui.Dim.Render("nothing picked"))
			}
			if id == "uninstall" {
				return capture("Uninstall", "", w, func() error { return pkgs.Uninstall(picked) })
			}
			return runTerm("Install", func() error { return pkgs.Install(picked) })
		}, huh.NewMultiSelect[string]().Title(label).Description("space to toggle, enter to confirm").Options(opts...).Value(&picked))
	case "theme":
		var opts []huh.Option[string]
		for _, n := range ui.ThemeNames {
			opts = append(opts, huh.NewOption(n, n))
		}
		name := ""
		return m.openForm("Theme", func() tea.Cmd {
			if err := ui.SetTheme(name); err != nil {
				return show("Theme", err.Error())
			}
			return show("Theme", "✓ theme set to "+name)
		}, huh.NewSelect[string]().Title("Color scheme").Options(opts...).Value(&name))
	case "machine":
		name := config.Machine()
		return m.openForm("Machine", func() tea.Cmd {
			if err := config.SetMachine(name); err != nil {
				return show("Machine", err.Error())
			}
			return show("Machine", "✓ machine set to "+name)
		}, huh.NewInput().Title("Machine name").Description("selects ##variants, e.g. server").Value(&name))
	}
	return nil
}

// pkgOptions lists this machine's packages for a multi-select; installedOnly is for uninstall.
func pkgOptions(installedOnly bool) ([]huh.Option[string], error) {
	ps, err := pkgs.Packages()
	if err != nil {
		return nil, err
	}
	var opts []huh.Option[string]
	for _, p := range ps {
		inst := pkgs.Installed(p)
		if installedOnly && !inst {
			continue
		}
		label := p.Name
		if inst {
			label += " ✓"
		}
		opts = append(opts, huh.NewOption(label, p.Name))
	}
	if len(opts) == 0 {
		return nil, errors.New("no packages")
	}
	return opts, nil
}

func show(title, body string) tea.Cmd {
	return func() tea.Msg { return contentMsg{title: title, body: body} }
}

// captureMu lets one report at a time point the shared ui.Out and ui.BoxWidth at itself:
// overlapping captures (a resize while a report runs) would restore each other's buffer for good.
var captureMu sync.Mutex

// capture runs a report in the background, drawn at width, and shows what it printed in the content pane.
func capture(title, view string, width int, fn func() error) tea.Cmd {
	return func() tea.Msg {
		captureMu.Lock()
		defer captureMu.Unlock()
		var b bytes.Buffer
		oldOut, oldWidth := ui.Out, ui.BoxWidth
		ui.Out, ui.BoxWidth = &b, width
		err := fn()
		ui.Out, ui.BoxWidth = oldOut, oldWidth
		if err != nil {
			b.WriteString("\n" + lipgloss.NewStyle().Foreground(ui.Err).Render("✗ "+err.Error()))
		}
		return contentMsg{title, view, b.String(), width}
	}
}

// termRun hands the real terminal to fn (git may ask for a password, installers print progress),
// then waits for Enter so its output can be read before the full-screen view comes back.
type termRun func() error

func (f termRun) Run() error {
	err := f()
	if err != nil && !errors.Is(err, huh.ErrUserAborted) {
		ui.SayErr(err)
	}
	fmt.Print(ui.Dim.Render("\n↩ press Enter to go back to homie "))
	bufio.NewReader(os.Stdin).ReadString('\n')
	return err
}
func (termRun) SetStdin(io.Reader)  {}
func (termRun) SetStdout(io.Writer) {}
func (termRun) SetStderr(io.Writer) {}

func runTerm(title string, fn func() error) tea.Cmd {
	return tea.Exec(termRun(fn), func(err error) tea.Msg { return doneMsg{title, err} })
}

func (m model) View() string {
	if m.w == 0 {
		return ""
	}
	body := m.contentView()
	if m.wide() {
		body = lipgloss.JoinHorizontal(lipgloss.Top, m.menuView(), " ", body)
	} else if !m.content {
		body = m.menuView()
	}
	hint := menu[m.cursor][2]
	if m.form != nil {
		hint = "esc cancel"
	}
	keys := "↑↓ move · enter open · pgup/pgdn scroll · q quit"
	switch {
	case m.form != nil:
		keys = "enter next · shift+tab back · esc cancel"
	case !m.wide():
		keys = "↑↓ move · enter open · esc back · q quit"
	}
	return lipgloss.JoinVertical(lipgloss.Left, m.header(),
		lipgloss.NewStyle().Height(m.bodyHeight()).MaxHeight(m.bodyHeight()).Render(body),
		trim(ui.Dim.Render(hint), m.w), trim(ui.Dim.Render(keys), m.w))
}

func trim(s string, w int) string { return lipgloss.NewStyle().MaxWidth(w).Render(s) }

func (m model) header() string {
	return ui.Frame.Width(max(m.w-2, 10)).Render(m.head)
}

func (m model) menuView() string {
	w := menuWidth
	if !m.wide() {
		w = m.w
	}
	rows := m.bodyHeight() - 2
	first := max(0, min(m.cursor-rows/2, len(menu)-rows)) // keep the cursor visible on short windows
	var lines []string
	for i := first; i < len(menu) && i < first+rows; i++ {
		label := "  " + menu[i][1]
		style := lipgloss.NewStyle().Foreground(ui.Text)
		if i == m.cursor {
			label = "› " + menu[i][1]
			style = style.Foreground(ui.Accent).Bold(true)
		}
		if !m.wide() && menu[i][2] != "" { // a full-width menu has room for the descriptions
			label = fmt.Sprintf("%-15s %s", label, ui.Dim.Render(menu[i][2]))
		}
		lines = append(lines, style.Render(label))
	}
	return ui.Frame.Width(w - 2).Height(rows).Render(strings.Join(lines, "\n"))
}

func (m model) contentView() string {
	w := m.contentWidth()
	title := lipgloss.NewStyle().Bold(true).Foreground(ui.TitleColor).Render(m.title)
	if m.form != nil {
		return title + "\n" + m.form.View()
	}
	return title + "\n" + lipgloss.NewStyle().MaxWidth(w).Render(m.vp.View())
}

// summary is the header text: version, machine, source, remote, and what is waiting.
func summary() string {
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
	if r := dotfiles.Rel(src); !strings.HasPrefix(r, "..") {
		src = "~/" + r
	}
	sep := ui.Dim.Render("  ·  ")
	label := func(k, v string) string { return ui.Dim.Render(k+" ") + v }
	return ui.Bold.Foreground(ui.Accent).Render("homie "+version) + sep +
		strings.Join([]string{label("machine", config.Machine()), label("files", files), label("packages", pk),
			label("source", src), label("remote", remote)}, sep)
}
