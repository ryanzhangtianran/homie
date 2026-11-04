package dotfiles

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ryanzhangtianran/homie/internal/config"
)

func TestSplitBranch(t *testing.T) {
	for in, want := range map[string][2]string{
		"me/dots@homie":                    {"me/dots", "homie"},
		"me/dots":                          {"me/dots", ""},
		"git@github.com:me/dots.git":       {"git@github.com:me/dots.git", ""},
		"git@github.com:me/dots.git@homie": {"git@github.com:me/dots.git", "homie"},
	} {
		if r, b := splitBranch(in); r != want[0] || b != want[1] {
			t.Errorf("splitBranch(%q) = %q, %q", in, r, b)
		}
	}
}

func TestRepoURL(t *testing.T) {
	for in, want := range map[string]string{
		"me/dotfiles":                    "https://github.com/me/dotfiles.git",
		"git@github.com:me/dotfiles.git": "git@github.com:me/dotfiles.git",
		"https://gitlab.com/me/dots.git": "https://gitlab.com/me/dots.git",
	} {
		if got := repoURL(in); got != want {
			t.Errorf("repoURL(%q) = %q", in, got)
		}
	}
}

func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func TestInit(t *testing.T) {
	h, src := sandbox(t, "server")
	os.RemoveAll(src) // Init wants to create it
	bare := filepath.Join(h, "remote.git")
	gitOut(t, h, "init", "-q", "--bare", bare)

	if err := Init(bare); err != nil { // existing repo: cloned
		t.Fatal(err)
	}
	if got := gitOut(t, src, "remote", "get-url", "origin"); got != bare {
		t.Fatalf("origin = %q", got)
	}

	os.RemoveAll(src)
	missing := filepath.Join(h, "missing.git")
	if err := Init(missing); err != nil { // missing repo: a new local one bound to it
		t.Fatal(err)
	}
	if !config.Exists(config.File()) || gitOut(t, src, "remote", "get-url", "origin") != missing {
		t.Fatal("missing repo should give a new local repo with a starter config, bound to it")
	}

	os.RemoveAll(src)
	if err := Init(bare + "@no-such-branch"); err == nil { // a failure that is not "repo missing"
		t.Fatal("a missing branch must be an error, not a new repo")
	}
	if config.Exists(src) {
		t.Fatal("failed Init left its directory behind")
	}
}
