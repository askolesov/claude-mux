// Package fsutil writes files whole or not at all: state files, installed
// backends, downloads and updates all go through it.
package fsutil

import (
	"io"
	"os"
	"path/filepath"
)

// WriteFile makes path anew: write fills a new file beside it, which then
// takes its place by rename. If write or anything after it fails, path is
// left as it was. Never a file rewritten in place: a crash halfway would
// leave half a state file, and macOS kills a binary whose code signature
// went stale. The directory must exist.
func WriteFile(path string, mode os.FileMode, write func(io.Writer) error) error {
	f, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(f.Name()) }() // gone already after the rename
	err = write(f)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Chmod(f.Name(), mode)
	}
	if err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

// CopyFile puts a copy of src at dst, by WriteFile.
func CopyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	return WriteFile(dst, mode, func(w io.Writer) error {
		_, err := io.Copy(w, in)
		return err
	})
}
