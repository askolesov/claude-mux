package main

import (
	"bytes"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"unicode/utf8"
)

var errNoTerminal = errors.New("its terminal app can't be scripted; only Terminal and iTerm2 can")

// typeScripts type keystroke chunks (arguments 2…) into the tab or pane that
// owns a tty (argument 1), each as its own write followed by a line feed,
// with a pause between them. Taking everything as arguments means no text is
// ever spliced into the script.
var typeScripts = map[string]string{
	"Terminal": `on run argv
	tell application "Terminal"
		repeat with w in windows
			repeat with t in tabs of w
				if tty of t is item 1 of argv then
					repeat with i from 2 to count of argv
						do script (item i of argv) in t
						delay 0.15
					end repeat
					return
				end if
			end repeat
		end repeat
	end tell
	error "no Terminal tab on " & item 1 of argv
end run`,
	"iTerm2": `on run argv
	tell application "iTerm2"
		repeat with w in windows
			repeat with t in tabs of w
				repeat with s in sessions of t
					if tty of s is item 1 of argv then
						repeat with i from 2 to count of argv
							tell s to write text ((item i of argv) & linefeed) newline no
							delay 0.15
						end repeat
						return
					end if
				end repeat
			end repeat
		end repeat
	end tell
	error "no iTerm2 session on " & item 1 of argv
end run`,
}

// TypeInto types text into the terminal window running pid and submits it.
func TypeInto(pid int, text string) error {
	if strings.TrimSpace(text) == "" {
		return errors.New("empty message")
	}
	out, err := exec.Command("ps", "-o", "tty=", "-p", strconv.Itoa(pid)).Output()
	tty := strings.TrimSpace(string(out))
	if err != nil || tty == "" || tty == "??" {
		return fmt.Errorf("process %d has no terminal", pid)
	}
	app, err := terminalApp(pid)
	if err != nil {
		return err
	}
	cmd := exec.Command("osascript", append([]string{"-", "/dev/" + tty}, keystrokeChunks(text)...)...)
	cmd.Stdin = strings.NewReader(typeScripts[app])
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s: %s", app, strings.TrimSpace(stderr.String()))
	}
	return nil
}

// renameKeys asks claude to rename the session: Ctrl+U clears whatever is
// typed in its prompt (one Ctrl+U per line end and per line, so 160 clear a
// draft of up to about 80 lines; an unsent draft is lost), then /rename.
// Claude's own name is the one the sidebar shows; it reports it in
// claude agents --json, and the tabs follow that.
func renameKeys(name string) (clear, command string) {
	return strings.Repeat("\x15", 160), "/rename " + strings.Join(strings.Fields(name), " ")
}

// chunkBytes keeps every write well under 64 bytes: claude reads a longer
// burst as a paste, where the closing Return becomes a newline instead of
// submitting.
const chunkBytes = 48

// keystrokeChunks splits text into writes that claude reads as typing. The
// scripts end every write with a line feed, which on its own submits the
// prompt, so each chunk but the last ends with ESC: ESC + LF is Alt+Enter, a
// newline in the prompt. A chunk that continues the same line starts with
// DEL (Backspace) to take that newline back; a chunk that starts a new line
// keeps it. The last chunk ends with CR, the Enter key.
func keystrokeChunks(text string) []string {
	var chunks []string
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		for i, piece := range splitBytes(line, chunkBytes) {
			if i > 0 {
				piece = "\x7f" + piece
			}
			chunks = append(chunks, piece+"\x1b")
		}
	}
	last := chunks[len(chunks)-1]
	chunks[len(chunks)-1] = strings.TrimSuffix(last, "\x1b") + "\r"
	return chunks
}

// splitBytes cuts s into pieces of at most n bytes, never inside a
// character. An empty s is one empty piece: a blank line still takes a write.
func splitBytes(s string, n int) []string {
	pieces := []string{}
	for len(s) > n {
		cut := n
		for !utf8.RuneStart(s[cut]) {
			cut--
		}
		pieces = append(pieces, s[:cut])
		s = s[cut:]
	}
	return append(pieces, s)
}

// terminalApp walks up from pid to the terminal app that owns it.
func terminalApp(pid int) (string, error) {
	for range 16 {
		out, err := exec.Command("ps", "-o", "ppid=,comm=", "-p", strconv.Itoa(pid)).Output()
		if err != nil {
			return "", errNoTerminal
		}
		ppid, comm, _ := strings.Cut(strings.TrimSpace(string(out)), " ")
		switch {
		case strings.Contains(comm, "/Terminal.app/"):
			return "Terminal", nil
		case strings.Contains(comm, "/iTerm.app/"):
			return "iTerm2", nil
		}
		if pid, err = strconv.Atoi(ppid); err != nil || pid <= 1 {
			return "", errNoTerminal
		}
	}
	return "", errNoTerminal
}

// OpenTerminal opens a new Terminal.app window and runs args there, in dir,
// in your login shell. The command reaches AppleScript as an argument, and
// every word is quoted for the shell.
func OpenTerminal(dir string, args []string) error {
	words := []string{"cd", shellQuote(dir), "&&"}
	for _, a := range args {
		words = append(words, shellQuote(a))
	}
	cmd := exec.Command("osascript", "-", strings.Join(words, " "))
	cmd.Stdin = strings.NewReader(`on run argv
	tell application "Terminal"
		do script (item 1 of argv)
		activate
	end tell
end run`)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("open Terminal: %s", strings.TrimSpace(stderr.String()))
	}
	return nil
}

// shellQuote wraps s in single quotes. A quote inside becomes '\” (close, escaped
// quote, reopen), which sh, zsh and fish all read the same way.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
