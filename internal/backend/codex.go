package backend

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// codexHookEvents are the Codex hook events that move a tab's status.
var codexHookEvents = []string{"SessionStart", "UserPromptSubmit", "PreToolUse", "PermissionRequest", "PostToolUse", "Stop", "Interrupt"}

// linkHook points <stateDir>/codex-hook at the running backend, exe, and
// returns that path. Codex trusts a hook by its command's text, so the
// command names this path, which stays the same across updates, instead of
// the backend's own, which changes with every version. The link is replaced
// by a rename, so a hook that fires meanwhile finds the old backend or the
// new one. Windows makes links only for admins, so there it is a copy.
func linkHook(stateDir, exe string) (string, error) {
	link := filepath.Join(stateDir, "codex-hook")
	tmp := link + ".new"
	_ = os.Remove(tmp)
	if runtime.GOOS == "windows" {
		link += ".exe"
		if same(link, exe) {
			return link, nil
		}
		data, err := os.ReadFile(exe)
		if err == nil {
			err = os.WriteFile(tmp, data, 0o700)
		}
		if err != nil {
			return "", err
		}
	} else if err := os.Symlink(exe, tmp); err != nil {
		return "", err
	}
	return link, os.Rename(tmp, link)
}

// codexHookCommand is the hook command codex sessions get: the stable link
// to exe, made now. If it can't be (on Windows, the old copy may be in use
// or being scanned), the old link serves, with the same text; with none,
// codex runs without hooks this time.
func codexHookCommand(stateDir, exe string) string {
	link, err := linkHook(stateDir, exe)
	if err == nil {
		return hookCommand(link)
	}
	fmt.Fprintln(os.Stderr, "linking the codex hook:", err)
	for _, old := range []string{filepath.Join(stateDir, "codex-hook"), filepath.Join(stateDir, "codex-hook.exe")} {
		if _, err := os.Lstat(old); err == nil {
			return hookCommand(old)
		}
	}
	return ""
}

// same reports whether two files hold the same bytes.
func same(a, b string) bool {
	x, err := os.ReadFile(a)
	if err != nil {
		return false
	}
	y, err := os.ReadFile(b)
	return err == nil && bytes.Equal(x, y)
}

// hookCommand is the command a hook runs: the backend at path, as `hook`.
// Hooks run under sh (Git Bash on Windows), which takes C:/… paths.
func hookCommand(path string) string {
	return shellQuote(filepath.ToSlash(path)) + " hook"
}

// codexArgs are codex's arguments: `resume <id>` to continue a conversation,
// the hooks for this session (-c, so nothing global is touched), the tab's
// own arguments, then the first prompt, after "--" so that one starting with
// "-" stays a prompt.
func codexArgs(hookCmd, sessionID string, args []string, prompt string) []string {
	var argv []string
	if sessionID != "" {
		argv = append(argv, "resume", sessionID)
	}
	for _, e := range codexHookEvents {
		if hookCmd == "" {
			break
		}
		argv = append(argv, "-c", fmt.Sprintf(`hooks.%s=[{hooks=[{type="command",command=%s}]}]`, e, tomlString(hookCmd)))
	}
	argv = append(argv, args...)
	if prompt != "" {
		argv = append(argv, "--", prompt)
	}
	return argv
}

// tomlString is s as a TOML basic string.
func tomlString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch {
		case r == '"' || r == '\\':
			b.WriteByte('\\')
			b.WriteRune(r)
		case r < 0x20 || r == 0x7f:
			fmt.Fprintf(&b, `\u%04X`, r)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}
