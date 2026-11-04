package pkgs

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
)

// walk calls fn for each regular file of a .zip/.tar.gz/.tgz download until fn returns false.
// archive is false when the download is a bare binary and fn was never called.
func walk(url string, data []byte, fn func(name string, mode fs.FileMode, read func() ([]byte, error)) (bool, error)) (archive bool, err error) {
	switch {
	case strings.HasSuffix(url, ".zip"):
		zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			return true, err
		}
		for _, f := range zr.File {
			if !f.Mode().IsRegular() {
				continue
			}
			read := func() ([]byte, error) {
				rc, err := f.Open()
				if err != nil {
					return nil, err
				}
				defer rc.Close()
				return io.ReadAll(rc)
			}
			if more, err := fn(f.Name, f.Mode(), read); err != nil || !more {
				return true, err
			}
		}
	case strings.HasSuffix(url, ".tar.gz"), strings.HasSuffix(url, ".tgz"):
		gz, err := gzip.NewReader(bytes.NewReader(data))
		if err != nil {
			return true, err
		}
		tr := tar.NewReader(gz)
		for {
			h, err := tr.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				return true, err
			}
			if h.Typeflag != tar.TypeReg {
				continue
			}
			if more, err := fn(h.Name, fs.FileMode(h.Mode), func() ([]byte, error) { return io.ReadAll(tr) }); err != nil || !more {
				return true, err
			}
		}
	default:
		return false, nil
	}
	return true, nil
}

// executables returns every executable file in the archive (a bare binary is saved as fallback).
func executables(url string, data []byte, fallback string) (map[string][]byte, error) {
	out := map[string][]byte{}
	archive, err := walk(url, data, func(name string, mode fs.FileMode, read func() ([]byte, error)) (bool, error) {
		if runtime.GOOS == "windows" && !strings.HasSuffix(name, ".exe") || runtime.GOOS != "windows" && mode&0o111 == 0 {
			return true, nil
		}
		b, err := read()
		out[path.Base(name)] = b
		return true, err
	})
	if err != nil {
		return nil, err
	}
	if !archive {
		out[fallback] = data
	}
	if len(out) == 0 {
		return nil, errors.New("no executables in " + path.Base(url) + "; set bins = [...]")
	}
	return out, nil
}

// extract returns member (matched by base name) from the archive, or the download itself for a bare binary.
func extract(url string, data []byte, member string) ([]byte, error) {
	var found []byte
	ok := false
	archive, err := walk(url, data, func(name string, _ fs.FileMode, read func() ([]byte, error)) (bool, error) {
		if path.Base(name) != member {
			return true, nil
		}
		b, err := read()
		found, ok = b, true
		return false, err
	})
	switch {
	case err != nil:
		return nil, err
	case !archive:
		return data, nil
	case !ok:
		return nil, errors.New(member + " not found in " + path.Base(url))
	}
	return found, nil
}

// replaceFile swaps in new content atomically; on Windows a running exe can be renamed but not overwritten.
func replaceFile(dst string, data []byte, mode fs.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	tmp := dst + ".new"
	if err := os.WriteFile(tmp, data, mode); err != nil {
		return err
	}
	if runtime.GOOS == "windows" {
		os.Remove(dst + ".old")
		if err := os.Rename(dst, dst+".old"); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return os.Rename(tmp, dst)
}
