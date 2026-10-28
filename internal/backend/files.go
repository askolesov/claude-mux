package backend

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// maxFile caps a file dropped or pasted into a tab.
const maxFile = 32 << 20

// filesDir is where a tab's dropped and pasted files are kept, in its
// environment, until the tab is closed.
func (m *Manager) filesDir(id string) string {
	return filepath.Join(filepath.Dir(m.stateFile), "files", id)
}

// SaveFile keeps a file dropped or pasted into a tab, so that its claude
// can read it, and returns its path. A name already there gets a number.
func (m *Manager) SaveFile(id, name string, data []byte) (string, error) {
	if _, err := m.Get(id); err != nil {
		return "", err
	}
	if len(data) > maxFile {
		return "", fmt.Errorf("%s is over %d MB", name, maxFile>>20)
	}
	name = filepath.Base(strings.ReplaceAll(name, `\`, "/"))
	if name == "." || name == "/" || name == ".." || name == "" {
		return "", errors.New("the file has no name")
	}
	dir := m.filesDir(id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	ext := filepath.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	for n := 1; ; n++ {
		path := filepath.Join(dir, name)
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if errors.Is(err, os.ErrExist) {
			name = fmt.Sprintf("%s %d%s", stem, n+1, ext)
			continue
		}
		if err != nil {
			return "", err
		}
		_, err = f.Write(data)
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			_ = os.Remove(path)
			return "", err
		}
		return path, nil
	}
}
