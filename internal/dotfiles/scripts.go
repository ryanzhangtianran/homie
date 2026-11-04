package dotfiles

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/ryanzhangtianran/homie/internal/config"
	"github.com/ryanzhangtianran/homie/internal/ui"
)

// runScripts runs run_* every apply and run_onchange_* only when its rendered content changed.
func runScripts(scripts []entry) error {
	hashes := map[string]string{}
	if err := config.ReadJSON("scripts.json", &hashes); err != nil {
		ui.Say(ui.Warn, "!", "note", err.Error()+"; run_onchange_ scripts will run again")
	}
	for _, s := range scripts {
		name := filepath.Base(s.dst)
		if (runtime.GOOS == "windows") != strings.HasSuffix(name, ".ps1") {
			continue // .ps1 scripts are for Windows, everything else for unix
		}
		sum := sha256.Sum256(s.data)
		h := hex.EncodeToString(sum[:])
		if strings.HasPrefix(name, "run_onchange_") && hashes[name] == h {
			continue
		}
		ui.Say(ui.Accent, "▶", "run", name)
		f, err := os.CreateTemp("", "homie-*-"+name)
		if err != nil {
			return err
		}
		f.Write(s.data)
		f.Close()
		os.Chmod(f.Name(), 0o700)
		var c *exec.Cmd
		if runtime.GOOS == "windows" {
			c = exec.Command("powershell", "-NoProfile", "-ExecutionPolicy", "Bypass", "-File", f.Name())
		} else if bytes.HasPrefix(s.data, []byte("#!")) {
			c = exec.Command(f.Name())
		} else {
			c = exec.Command("sh", f.Name())
		}
		c.Dir, c.Stdin, c.Stdout, c.Stderr = config.Home(), os.Stdin, os.Stdout, os.Stderr
		err = c.Run()
		os.Remove(f.Name())
		if err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		hashes[name] = h
		if err := config.WriteJSON("scripts.json", hashes); err != nil {
			return err
		}
	}
	return nil
}
