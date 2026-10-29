package backend

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

func TestCodexHookTextStable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("a copy, not a link, on Windows")
	}
	dir := t.TempDir()
	v1, v2 := filepath.Join(dir, "v1"), filepath.Join(dir, "v2")
	for _, p := range []string{v1, v2} {
		if err := os.WriteFile(p, nil, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	l1, err := linkHook(dir, v1)
	if err != nil {
		t.Fatal(err)
	}
	l2, err := linkHook(dir, v2)
	if err != nil {
		t.Fatal(err)
	}
	if hookCommand(l1) != hookCommand(l2) {
		t.Errorf("the hook text changed with the backend: %q, %q", hookCommand(l1), hookCommand(l2))
	}
	if to, _ := os.Readlink(l2); to != v2 {
		t.Errorf("the link points at %q, want %q", to, v2)
	}
}

func TestCodexArgs(t *testing.T) {
	hook := "'/s/codex-hook' hook"
	fresh := codexArgs(hook, "", []string{"--search"}, "")
	if fresh[0] != "-c" || fresh[len(fresh)-1] != "--search" {
		t.Errorf("fresh %q", fresh)
	}
	if !slices.Contains(fresh, `hooks.Stop=[{hooks=[{type="command",command="'/s/codex-hook' hook"}]}]`) {
		t.Errorf("no Stop hook in %q", fresh)
	}
	again := codexArgs(hook, "u1", nil, "-x go")
	if !slices.Equal(again[:2], []string{"resume", "u1"}) || !slices.Equal(again[len(again)-2:], []string{"--", "-x go"}) {
		t.Errorf("continued %q", again)
	}
	odd := codexArgs(`'/a "b\c/codex-hook' hook`, "", nil, "")
	if !slices.ContainsFunc(odd, func(s string) bool { return strings.Contains(s, `command="'/a \"b\\c/codex-hook' hook"`) }) {
		t.Errorf("not a TOML basic string: %q", odd)
	}
}

// A hook link that can't be replaced (Windows: the old copy in use) keeps
// the old one: the text is the same.
func TestLinkHookKeepsOldOnFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix links")
	}
	dir := t.TempDir()
	exe := filepath.Join(dir, "v1")
	if err := os.WriteFile(exe, nil, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := linkHook(dir, exe); err != nil {
		t.Fatal(err)
	}
	// A directory where the temp link goes makes the next link fail.
	if err := os.Mkdir(filepath.Join(dir, "codex-hook.new"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "codex-hook.new", "x"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := codexHookCommand(dir, filepath.Join(dir, "v2"))
	if cmd != hookCommand(filepath.Join(dir, "codex-hook")) {
		t.Errorf("hook %q", cmd)
	}
}
