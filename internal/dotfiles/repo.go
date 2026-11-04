package dotfiles

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/ryanzhangtianran/homie/internal/config"
	"github.com/ryanzhangtianran/homie/internal/ui"
)

func Git(args ...string) error {
	c := exec.Command("git", append([]string{"-C", config.SrcDir()}, args...)...)
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

// Init clones repo[@branch] if it exists, otherwise creates a local repo (with a starter .homie.toml) bound to it.
func Init(spec string) error {
	if config.Exists(config.SrcDir()) {
		return fmt.Errorf("%s already exists (use `homie remote` to change the binding)", config.SrcDir())
	}
	repo, branch := splitBranch(spec)
	if repo != "" && remoteExists(repoURL(repo)) {
		args := []string{"clone", repoURL(repo), config.SrcDir()}
		if branch != "" {
			args = append(args, "--branch", branch)
		}
		c := exec.Command("git", args...)
		c.Stdout, c.Stderr = os.Stdout, os.Stderr
		return c.Run()
	}
	if err := os.MkdirAll(config.SrcDir(), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(config.File(), []byte(config.Default), 0o644); err != nil {
		return err
	}
	if err := Git("init", "-q"); err != nil {
		return err
	}
	if branch != "" {
		if err := Git("checkout", "-q", "-b", branch); err != nil {
			return err
		}
	}
	if repo == "" {
		return nil
	}
	return Bind(repo)
}

// Bind points origin at repo, creating it on GitHub (private) via gh when it does not exist yet.
func Bind(repo string) error {
	url := repoURL(repo)
	if exec.Command("git", "-C", config.SrcDir(), "remote", "get-url", "origin").Run() == nil {
		if err := Git("remote", "set-url", "origin", url); err != nil {
			return err
		}
	} else if err := Git("remote", "add", "origin", url); err != nil {
		return err
	}
	ui.Say(ui.OK, "✓", "bound", url)
	if remoteExists(url) {
		return nil
	}
	if _, err := exec.LookPath("gh"); err == nil && url != repo {
		c := exec.Command("gh", "repo", "create", repo, "--private")
		c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
		return c.Run()
	}
	ui.Say(ui.Warn, "!", "note", "repo not found: create it on GitHub, then run `homie push`")
	return nil
}

func Push(msg string) error {
	if err := Git("add", "-A"); err != nil {
		return err
	}
	if exec.Command("git", "-C", config.SrcDir(), "diff", "--cached", "--quiet").Run() != nil { // staged changes exist
		if err := Git("commit", "-q", "-m", msg); err != nil {
			return err
		}
	}
	return Git("push", "-u", "origin", "HEAD")
}
