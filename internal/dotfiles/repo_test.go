package dotfiles

import "testing"

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
