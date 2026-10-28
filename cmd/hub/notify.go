//go:build cgo

package main

import (
	"fmt"
	"os"

	"github.com/gen2brain/beeep"
)

func init() { beeep.AppName = "claude-mux" }

// notify posts a notification for the page: the app's web view has no
// Notification API of its own. beeep takes the system's way: AppleScript on
// a Mac, a toast on Windows, D-Bus (libnotify) on Linux.
func notify(title, body string) {
	go func() {
		if err := beeep.Notify(title, body, ""); err != nil {
			fmt.Fprintln(os.Stderr, "claude-mux: notification:", err)
		}
	}()
}
