package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

// ReadJSON loads a state file into v; a missing file leaves v as it is, a damaged one is an error.
func ReadJSON(name string, v any) error {
	data, err := os.ReadFile(filepath.Join(StateDir(), name))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err == nil {
		err = json.Unmarshal(data, v)
	}
	if err != nil {
		return fmt.Errorf("state file %s: %w", name, err)
	}
	return nil
}

// WriteJSON saves v via a temp file and rename, so a crash mid-write never leaves half a file.
func WriteJSON(name string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(StateDir(), 0o755); err != nil {
		return err
	}
	p := filepath.Join(StateDir(), name)
	if err := os.WriteFile(p+".tmp", data, 0o644); err != nil {
		return err
	}
	return os.Rename(p+".tmp", p)
}

func Home() string { h, _ := os.UserHomeDir(); return h }

func SrcDir() string {
	if d := os.Getenv("HOMIE_SOURCE"); d != "" {
		return d
	}
	return filepath.Join(Home(), ".local", "share", "homie")
}

func StateDir() string { return filepath.Join(Home(), ".local", "state", "homie") }

func Exists(p string) bool { _, err := os.Stat(p); return err == nil }

func Shell(script string) *exec.Cmd {
	if runtime.GOOS == "windows" {
		return exec.Command("powershell", "-NoProfile", "-ExecutionPolicy", "Bypass", "-Command", script)
	}
	return exec.Command("sh", "-c", script)
}

func shellOutput(script string) (string, error) {
	c := Shell(script)
	c.Stderr = os.Stderr
	out, err := c.Output()
	return string(out), err
}
