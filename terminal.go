package main

import (
	"bytes"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

var errNoTerminal = errors.New("its terminal app can't be scripted; only Terminal and iTerm2 can")

// typeScripts type a line into the tab or pane that owns a tty and press
// Return, as if you typed it there. They take the tty and the text as
// arguments, so neither is ever spliced into the script.
//
// Terminal's do script ends the text with a line feed, which a program in
// raw mode (claude) reads as Ctrl+J, a newline in the prompt. The explicit
// return (CR) before it is the Enter key.
var typeScripts = map[string]string{
	"Terminal": `on run argv
	tell application "Terminal"
		repeat with w in windows
			repeat with t in tabs of w
				if tty of t is item 1 of argv then
					do script (item 2 of argv) & return in t
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
						tell s to write text (item 2 of argv)
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
// Line breaks become spaces: each would submit a prompt of its own.
func TypeInto(pid int, text string) error {
	text = strings.Join(strings.Fields(text), " ")
	if text == "" {
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
	cmd := exec.Command("osascript", "-", "/dev/"+tty, text)
	cmd.Stdin = strings.NewReader(typeScripts[app])
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s: %s", app, strings.TrimSpace(stderr.String()))
	}
	return nil
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
