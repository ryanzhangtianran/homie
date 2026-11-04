package ui

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// sandbox gives each test its own $HOME and source dir.
func sandbox(t *testing.T, machineName string) (h, src string) {
	h = t.TempDir()
	src = filepath.Join(h, "src")
	t.Setenv("HOME", h)
	t.Setenv("USERPROFILE", h)
	t.Setenv("HOMIE_SOURCE", src)
	t.Setenv("HOMIE_MACHINE", machineName)
	os.MkdirAll(src, 0o755)
	return
}

func stripANSI(s string) string { return regexp.MustCompile(`\x1b\[[0-9;]*m`).ReplaceAllString(s, "") }
