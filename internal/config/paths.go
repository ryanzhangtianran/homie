package config

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

func LoadJSON(name string) map[string]string {
	m := map[string]string{}
	data, _ := os.ReadFile(filepath.Join(StateDir(), name))
	_ = json.Unmarshal(data, &m)
	return m
}

func SaveJSON(name string, m map[string]string) error {
	data, _ := json.MarshalIndent(m, "", "  ")
	if err := os.MkdirAll(StateDir(), 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(StateDir(), name), data, 0o644)
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
