//go:build cgo

package main

import "os/exec"

// notify posts a notification for the page, which in a web view of its own
// has no Notification API. The text goes as arguments, never into the script.
func notify(title, body string) {
	cmd := exec.Command("osascript",
		"-e", "on run argv", "-e", "display notification (item 2 of argv) with title (item 1 of argv)", "-e", "end run",
		title, body)
	if cmd.Start() == nil {
		go func() { _ = cmd.Wait() }()
	}
}
