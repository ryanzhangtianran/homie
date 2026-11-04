package main

import (
	"fmt"
	"io"
	"net/http"
	"path"
	"regexp"
	"strings"
)

var (
	osWords = map[string][]string{
		"linux":   {"linux"},
		"darwin":  {"darwin", "apple", "macos", "osx", "mac"},
		"windows": {"windows", "win64", "-win"},
	}
	archWords = map[string][]string{
		"amd64": {"x86_64", "amd64", "x64"},
		"arm64": {"aarch64", "arm64"},
	}
	// platforms nobody here runs, and files that are not the program itself
	foreign = []string{"freebsd", "netbsd", "openbsd", "android", "illumos", "solaris", "dragonfly",
		"i686", "i386", "386", "armv", "armhf", "arm32", "arm-", "riscv", "s390", "ppc", "loong", "mips"}
	junk = []string{".sha", ".sig", ".asc", ".pem", ".sbom", ".json", ".txt", ".md", ".deb", ".rpm", ".apk",
		".msi", ".pkg", ".dmg", ".appimage", ".xz", ".bz2", ".zst", ".7z", ".b3", ".vsix", ".whl", ".ps1", ".sh",
		"checksum", "installer", "source"}
)

func containsAny(s string, words []string) bool {
	for _, w := range words {
		if strings.Contains(s, w) {
			return true
		}
	}
	return false
}

func usableFormat(name, goos string) bool {
	switch {
	case strings.HasSuffix(name, ".tar.gz"), strings.HasSuffix(name, ".tgz"), strings.HasSuffix(name, ".zip"):
		return true
	case strings.HasSuffix(name, ".exe"):
		return goos == "windows"
	}
	ext := path.Ext(name) // a bare binary: no extension, or only version dots ("tool-1.2.3-linux_amd64")
	return goos != "windows" && (ext == "" || strings.ContainsAny(ext, "-_"))
}

// pickAsset chooses the release asset for goos/goarch by name. Names without an OS are taken as
// Linux builds (fnm-arm64.zip); musl beats glibc (runs on old distros), msvc beats mingw.
func pickAsset(names []string, goos, goarch string) (string, error) {
	best, bestScore := "", 0
	for _, n := range names {
		l := strings.ToLower(n)
		if containsAny(l, junk) || containsAny(l, foreign) || !usableFormat(l, goos) {
			continue
		}
		otherOS := false
		for o, w := range osWords {
			if o != goos && containsAny(l, w) {
				otherOS = true
			}
		}
		if otherOS {
			continue
		}
		s := 0
		if containsAny(l, osWords[goos]) {
			s += 2
		} else if goos != "linux" {
			continue
		}
		switch other := archWords[map[string]string{"amd64": "arm64", "arm64": "amd64"}[goarch]]; {
		case containsAny(l, archWords[goarch]), goos == "darwin" && strings.Contains(l, "universal"):
			s += 4
		case containsAny(l, other):
			if goos != "darwin" { // Apple silicon runs x86_64 builds through Rosetta, so keep them as a last resort
				continue
			}
		default:
			s++
		}
		if strings.Contains(l, "musl") || strings.Contains(l, "msvc") {
			s++
		}
		if s > bestScore || s == bestScore && len(n) < len(best) {
			best, bestScore = n, s
		}
	}
	if best == "" {
		return "", fmt.Errorf("no release asset for %s/%s; set url = ... for this package", goos, goarch)
	}
	return best, nil
}

var assetHref = regexp.MustCompile(`/releases/download/[^/"]+/([^"]+)"`)

// listAssets reads asset names from the release page GitHub's UI uses (no API token, no rate limit).
func listAssets(repo, tag string) ([]string, error) {
	resp, err := http.Get("https://github.com/" + repo + "/releases/expanded_assets/" + tag)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, m := range assetHref.FindAllSubmatch(data, -1) {
		names = append(names, string(m[1]))
	}
	if len(names) == 0 {
		return nil, fmt.Errorf("no assets in %s release %s (status %s)", repo, tag, resp.Status)
	}
	return names, nil
}
