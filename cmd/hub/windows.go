package main

import (
	"os"
	"os/exec"
	"sync"
)

// windowProcs are the windows the hub opened as processes of their own, on
// Windows and Linux, where a second web view in one process can stay blank.
// Each is this program started again: it finds the hub's lock held and shows
// the running hub's page.
type windowProcs struct{ wg sync.WaitGroup }

func (w *windowProcs) start() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.Command(exe, os.Args[1:]...)
	cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	w.wg.Add(1)
	go func() {
		defer w.wg.Done()
		_ = cmd.Wait() // a window closed, however it ended
	}()
	return nil
}

// wait returns once every window the hub opened is closed.
func (w *windowProcs) wait() { w.wg.Wait() }
