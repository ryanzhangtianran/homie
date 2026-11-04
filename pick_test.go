package main

import (
	"os"
	"strings"
	"testing"
)

// testdata/*.txt are real asset lists of those projects' latest releases.
func TestPickAsset(t *testing.T) {
	want := map[string]map[string]string{
		"fnm": {
			"linux/amd64": "fnm-linux.zip", "linux/arm64": "fnm-arm64.zip",
			"darwin/arm64": "fnm-macos.zip", "darwin/amd64": "fnm-macos.zip", "windows/amd64": "fnm-windows.zip",
		},
		"starship": {
			"linux/amd64": "starship-x86_64-unknown-linux-musl.tar.gz", "linux/arm64": "starship-aarch64-unknown-linux-musl.tar.gz",
			"darwin/arm64": "starship-aarch64-apple-darwin.tar.gz", "darwin/amd64": "starship-x86_64-apple-darwin.tar.gz",
			"windows/amd64": "starship-x86_64-pc-windows-msvc.zip", "windows/arm64": "starship-aarch64-pc-windows-msvc.zip",
		},
		"fzf": {
			"linux/amd64": "fzf-0.74.5-linux_amd64.tar.gz", "linux/arm64": "fzf-0.74.5-linux_arm64.tar.gz",
			"darwin/arm64": "fzf-0.74.5-darwin_arm64.tar.gz", "windows/amd64": "fzf-0.74.5-windows_amd64.zip",
		},
		"ripgrep": {
			"linux/amd64": "ripgrep-15.2.0-x86_64-unknown-linux-musl.tar.gz", "linux/arm64": "ripgrep-15.2.0-aarch64-unknown-linux-musl.tar.gz",
			"darwin/arm64": "ripgrep-15.2.0-aarch64-apple-darwin.tar.gz", "windows/amd64": "ripgrep-15.2.0-x86_64-pc-windows-msvc.zip",
		},
		"uv": {
			"linux/amd64": "uv-x86_64-unknown-linux-musl.tar.gz", "linux/arm64": "uv-aarch64-unknown-linux-musl.tar.gz",
			"darwin/arm64": "uv-aarch64-apple-darwin.tar.gz", "windows/amd64": "uv-x86_64-pc-windows-msvc.zip",
		},
		"zoxide": {
			"linux/amd64": "zoxide-0.10.0-x86_64-unknown-linux-musl.tar.gz", "darwin/arm64": "zoxide-0.10.0-aarch64-apple-darwin.tar.gz",
			"windows/amd64": "zoxide-0.10.0-x86_64-pc-windows-msvc.zip",
		},
		"atuin": {
			"linux/amd64": "atuin-x86_64-unknown-linux-musl.tar.gz", "linux/arm64": "atuin-aarch64-unknown-linux-musl.tar.gz",
			"darwin/arm64": "atuin-aarch64-apple-darwin.tar.gz",
		},
		"bat": {
			"linux/amd64": "bat-v0.26.1-x86_64-unknown-linux-musl.tar.gz", "linux/arm64": "bat-v0.26.1-aarch64-unknown-linux-musl.tar.gz",
			"darwin/arm64": "bat-v0.26.1-aarch64-apple-darwin.tar.gz", "windows/amd64": "bat-v0.26.1-x86_64-pc-windows-msvc.zip",
		},
	}
	for pkg, cases := range want {
		data, err := os.ReadFile("testdata/" + pkg + ".txt")
		if err != nil {
			t.Fatal(err)
		}
		names := strings.Fields(string(data))
		for plat, exp := range cases {
			goos, goarch, _ := strings.Cut(plat, "/")
			if got, err := pickAsset(names, goos, goarch); got != exp {
				t.Errorf("%s %s: got %q (%v), want %q", pkg, plat, got, err, exp)
			}
		}
	}
	if _, err := pickAsset([]string{"tool-linux-amd64.tar.gz"}, "windows", "amd64"); err == nil {
		t.Error("expected no match for windows")
	}
}
