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

// executables returns every executable regular file in the archive (a bare binary is saved as fallback).
func executables(url string, data []byte, fallback string) (map[string][]byte, error) {
	out := map[string][]byte{}
	isExe := func(name string, mode fs.FileMode) bool {
		if runtime.GOOS == "windows" {
			return strings.HasSuffix(name, ".exe")
		}
		return mode&0o111 != 0
	}
	switch {
	case strings.HasSuffix(url, ".zip"):
		zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			return nil, err
		}
		for _, f := range zr.File {
			if f.Mode().IsRegular() && isExe(f.Name, f.Mode()) {
				rc, err := f.Open()
				if err != nil {
					return nil, err
				}
				b, err := io.ReadAll(rc)
				rc.Close()
				if err != nil {
					return nil, err
				}
				out[path.Base(f.Name)] = b
			}
		}
	case strings.HasSuffix(url, ".tar.gz"), strings.HasSuffix(url, ".tgz"):
		gz, err := gzip.NewReader(bytes.NewReader(data))
		if err != nil {
			return nil, err
		}
		tr := tar.NewReader(gz)
		for {
			h, err := tr.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				return nil, err
			}
			if h.Typeflag == tar.TypeReg && isExe(h.Name, fs.FileMode(h.Mode)) {
				b, err := io.ReadAll(tr)
				if err != nil {
					return nil, err
				}
				out[path.Base(h.Name)] = b
			}
		}
	default:
		out[fallback] = data
	}
	if len(out) == 0 {
		return nil, errors.New("no executables in " + path.Base(url) + "; set bins = [...]")
	}
	return out, nil
}

// extract returns member (matched by base name) from a .zip/.tar.gz/.tgz archive, or the download itself for a bare binary.
func extract(url string, data []byte, member string) ([]byte, error) {
	switch {
	case strings.HasSuffix(url, ".zip"):
		zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			return nil, err
		}
		for _, f := range zr.File {
			if path.Base(f.Name) == member && !f.FileInfo().IsDir() {
				rc, err := f.Open()
				if err != nil {
					return nil, err
				}
				defer rc.Close()
				return io.ReadAll(rc)
			}
		}
	case strings.HasSuffix(url, ".tar.gz"), strings.HasSuffix(url, ".tgz"):
		gz, err := gzip.NewReader(bytes.NewReader(data))
		if err != nil {
			return nil, err
		}
		tr := tar.NewReader(gz)
		for {
			h, err := tr.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				return nil, err
			}
			if path.Base(h.Name) == member && h.Typeflag == tar.TypeReg {
				return io.ReadAll(tr)
			}
		}
	default:
		return data, nil
	}
	return nil, errors.New(member + " not found in " + path.Base(url))
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
