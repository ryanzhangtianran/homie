package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

func TestColumns(t *testing.T) {
	out := Columns([]string{"A", "LONG HEADER"}, [][]string{{Dim.Render("x"), "1"}, {"yyyy", "2"}, {"z", "last"}})
	lines := strings.Split(out, "\n")
	if len(lines) != 5 || !strings.Contains(lines[4], "last") { // header + rule + 3 rows, last row kept
		t.Fatalf("%q", lines)
	}
	col := func(l string) int { return lipgloss.Width(l) - len(strings.TrimLeft(stripANSI(l)[4:], " ")) }
	if col(lines[2]) != col(lines[3]) || col(lines[3]) != col(lines[4]) {
		t.Fatalf("second column not aligned:\n%s", out)
	}
}

func TestTheme(t *testing.T) {
	sandbox(t, "server")
	defer usePalette(palettes["catppuccin"])
	if err := SetTheme("nope"); err == nil {
		t.Fatal("unknown theme accepted")
	}
	if err := SetTheme("nord"); err != nil || Accent != palettes["nord"].Accent {
		t.Fatal(err)
	}
	usePalette(palettes["catppuccin"])
	LoadTheme() // reads the saved choice back
	if Accent != palettes["nord"].Accent {
		t.Fatal("saved theme not loaded")
	}
}
