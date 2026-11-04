package dotfiles

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
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

// ghExists asks gh (which carries your GitHub login) whether repo exists, so private repos are seen too.
// known is false when gh is missing, logged out, or repo is not a user/repo shorthand.
func ghExists(repo string) (exists, known bool) {
	if _, err := exec.LookPath("gh"); err != nil || repoURL(repo) == repo {
		return false, false
	}
	out, err := exec.Command("gh", "repo", "view", repo, "--json", "name").CombinedOutput()
	if err == nil {
		return true, true
	}
	return false, strings.Contains(string(out), "Could not resolve to a Repository")
}

// repoMissing matches git's "no such repository" errors (GitHub's and a local path's), not a missing branch.
var repoMissing = regexp.MustCompile(`repository not found|repository '[^']*' (not found|does not exist)`)

// clone clones repo; notFound is true only when git says the repository does not exist
// (any other failure, such as a refused login, must not be mistaken for "create a new one").
func clone(repo, branch string) (notFound bool, err error) {
	args := []string{"clone", repoURL(repo), config.SrcDir()}
	if exists, known := ghExists(repo); known && exists {
		args = []string{"repo", "clone", repo, config.SrcDir()} // uses gh's login for private repos
		if branch != "" {
			args = append(args, "--", "--branch", branch)
		}
		c := exec.Command("gh", args...)
		c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
		return false, c.Run()
	} else if known {
		return true, errors.New("not found")
	}
	if branch != "" {
		args = append(args, "--branch", branch)
	}
	var stderr strings.Builder
	c := exec.Command("git", args...) // may ask for a password: that is how a private repo gets cloned without gh
	c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, io.MultiWriter(os.Stderr, &stderr)
	if err := c.Run(); err != nil {
		return repoMissing.MatchString(strings.ToLower(stderr.String())), err
	}
	return false, nil
}

// splitBranch splits "user/repo@branch"; the @ must come after the last "/" so git@host:... is left alone.
func splitBranch(s string) (repo, branch string) {
	if i := strings.LastIndex(s, "@"); i > strings.LastIndex(s, "/") {
		return s[:i], s[i+1:]
	}
	return s, ""
}

// Init clones repo[@branch]; only when it does not exist yet does it create a local repo (with a starter
// .homie.toml) bound to it. On failure it removes what it created, so it can simply be run again.
func Init(spec string) (err error) {
	if config.Exists(config.SrcDir()) {
		return fmt.Errorf("%s already exists (use `homie remote` to change the binding)", config.SrcDir())
	}
	repo, branch := splitBranch(spec)
	if repo != "" {
		notFound, err := clone(repo, branch)
		if err == nil {
			return nil
		}
		if !notFound {
			return fmt.Errorf("clone %s: %w (private repo: run `gh auth login` first)", repo, err)
		}
		ui.Say(ui.Muted, "·", "new repo", repo+" does not exist yet, creating it")
	}
	defer func() {
		if err != nil {
			os.RemoveAll(config.SrcDir()) // it did not exist when we started
		}
	}()
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
	exists, known := ghExists(repo)
	if exists || !known && remoteExists(url) {
		return nil
	}
	if known { // gh is logged in and says the repo is not there
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
