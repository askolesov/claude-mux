package backend

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestCompleteDir(t *testing.T) {
	root := t.TempDir()
	for _, d := range []string{"src/billing", "src/bin", "src/docs", "src/.cache"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "src", "binary"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	sep := string(filepath.Separator)
	src := filepath.Join(root, "src") + sep
	for _, c := range []struct {
		prefix string
		want   []string
	}{
		{src + "bi", []string{src + "billing" + sep, src + "bin" + sep}},
		{src, []string{src + "billing" + sep, src + "bin" + sep, src + "docs" + sep}},
		{src + ".", []string{src + ".cache" + sep}},
		{src + "x", []string{}},
		{"", []string{}},
		{filepath.Join(root, "missing") + sep, []string{}},
	} {
		if got := completeDir(c.prefix); !slices.Equal(got, c.want) {
			t.Errorf("%q: got %q, want %q", c.prefix, got, c.want)
		}
	}
}
