package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

func git(args ...string) error {
	c := exec.Command("git", append([]string{"-C", srcDir()}, args...)...)
	c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
	return c.Run()
}

// repoURL expands "user/repo" to a GitHub URL; full git URLs (https or git@...) pass through.
func repoURL(s string) string {
	if strings.Count(s, "/") == 1 && !strings.Contains(s, ":") {
		return "https://github.com/" + strings.TrimSuffix(s, ".git") + ".git"
	}
	return s
}

func remoteExists(url string) bool {
	c := exec.Command("git", "ls-remote", url)
	c.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0") // a check must never hang on a password prompt
	return c.Run() == nil
}

// splitBranch splits "user/repo@branch"; the @ must come after the last "/" so git@host:... is left alone.
func splitBranch(s string) (repo, branch string) {
	if i := strings.LastIndex(s, "@"); i > strings.LastIndex(s, "/") {
		return s[:i], s[i+1:]
	}
	return s, ""
}

// initSource clones repo[@branch] if it exists, otherwise creates a local repo (with a starter .homie.toml) bound to it.
func initSource(spec string) error {
	if dirExists(srcDir()) {
		return fmt.Errorf("%s already exists (use `homie remote` to change the binding)", srcDir())
	}
	repo, branch := splitBranch(spec)
	if repo != "" && remoteExists(repoURL(repo)) {
		args := []string{"clone", repoURL(repo), srcDir()}
		if branch != "" {
			args = append(args, "--branch", branch)
		}
		c := exec.Command("git", args...)
		c.Stdout, c.Stderr = os.Stdout, os.Stderr
		return c.Run()
	}
	if err := os.MkdirAll(srcDir(), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(configFile(), []byte(defaultConfig), 0o644); err != nil {
		return err
	}
	if err := git("init", "-q"); err != nil {
		return err
	}
	if branch != "" {
		if err := git("checkout", "-q", "-b", branch); err != nil {
			return err
		}
	}
	if repo == "" {
		return nil
	}
	return bind(repo)
}

// bind points origin at repo, creating it on GitHub (private) via gh when it does not exist yet.
func bind(repo string) error {
	url := repoURL(repo)
	if exec.Command("git", "-C", srcDir(), "remote", "get-url", "origin").Run() == nil {
		if err := git("remote", "set-url", "origin", url); err != nil {
			return err
		}
	} else if err := git("remote", "add", "origin", url); err != nil {
		return err
	}
	say(cOK, "✓", "bound", url)
	if remoteExists(url) {
		return nil
	}
	if _, err := exec.LookPath("gh"); err == nil && url != repo {
		c := exec.Command("gh", "repo", "create", repo, "--private")
		c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
		return c.Run()
	}
	say(cWarn, "!", "note", "repo not found: create it on GitHub, then run `homie push`")
	return nil
}

func push(msg string) error {
	if err := git("add", "-A"); err != nil {
		return err
	}
	if exec.Command("git", "-C", srcDir(), "diff", "--cached", "--quiet").Run() != nil { // staged changes exist
		if err := git("commit", "-q", "-m", msg); err != nil {
			return err
		}
	}
	return git("push", "-u", "origin", "HEAD")
}
