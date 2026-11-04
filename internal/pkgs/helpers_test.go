package pkgs

import (
	"os"
	"path/filepath"
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

func read(p string) string { b, _ := os.ReadFile(p); return string(b) }
