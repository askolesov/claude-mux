package backend

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// maxDirs caps what completeDir returns; typing more narrows it.
const maxDirs = 50

// completeDir lists the directories whose path starts with prefix, the
// way a shell completes one on Tab: prefix's directory part is read, its
// last part matched. They come back written as prefix was, with "~" kept,
// and a "/" at the end. Hidden directories show only once a "." is typed.
func completeDir(prefix string) []string {
	if prefix == "" {
		return []string{}
	}
	dir, part := filepath.Split(prefix)
	if dir == "" {
		return []string{}
	}
	entries, err := os.ReadDir(expandHome(dir))
	if err != nil {
		return []string{}
	}
	out := []string{}
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, part) || (strings.HasPrefix(name, ".") && !strings.HasPrefix(part, ".")) {
			continue
		}
		if !e.IsDir() {
			// A link to a directory counts.
			if st, err := os.Stat(filepath.Join(expandHome(dir), name)); err != nil || !st.IsDir() {
				continue
			}
		}
		out = append(out, dir+name+string(filepath.Separator))
	}
	slices.Sort(out)
	if len(out) > maxDirs {
		out = out[:maxDirs]
	}
	return out
}

// expandHome turns a leading "~" into the home directory.
func expandHome(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, p[1:])
		}
	}
	return p
}
