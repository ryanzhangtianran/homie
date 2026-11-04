package main

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

func write(t *testing.T, p, s string, mode os.FileMode) {
	t.Helper()
	os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.WriteFile(p, []byte(s), mode); err != nil {
		t.Fatal(err)
	}
}

func stripANSI(s string) string { return regexp.MustCompile(`\x1b\[[0-9;]*m`).ReplaceAllString(s, "") }
