//go:build cgo

package main

import (
	"context"
	"runtime"

	webview "github.com/webview/webview_go"
)

// The windows are made and run on the main thread, as macOS insists.
func init() { runtime.LockOSThread() }

// window shows the page in a window of its own, the system's web view, and
// in any more the page opens (New window, ⌘N), until the last is closed or
// ctx ends.
func window(ctx context.Context, url string) {
	// Everything here runs on the main thread: the first open below, and
	// every later one through Dispatch, which runs it on the main thread's
	// message loop. So no locks; the hazard is re-entry, not threads.
	var all []webview.WebView
	// Making a window waits for its web view inside a message loop of its
	// own, which runs dispatched calls too: a New window handled there would
	// start a web view inside another's setup. So a call while one is being
	// made only counts itself (queued), and the outer call makes it once the
	// window before is done.
	making, queued := false, 0
	var open, make1 func()
	open = func() {
		if making {
			queued++
			return
		}
		making = true
		for make1(); queued > 0; queued-- {
			make1()
		}
		making = false
	}
	make1 = func() {
		w := webview.New(false)
		all = append(all, w)
		unloadOnClose(w.Window())
		w.SetTitle("claude-mux")
		w.SetSize(1400, 900, webview.HintNone)
		_ = w.Bind("cmuxNotify", notify)
		_ = w.Bind("cmuxOpen", openLink)
		_ = w.Bind("cmuxPaths", filePaths)
		_ = w.Bind("cmuxBadge", func(label string) { w.Dispatch(func() { badge(label) }) })
		_ = w.Bind("cmuxNewWindow", func() { w.Dispatch(open) })
		w.Navigate(url)
	}
	open()
	nativeMenu()
	first := all[0]
	closed := make(chan struct{})
	defer close(closed)
	go func() {
		select {
		case <-ctx.Done():
			first.Dispatch(first.Terminate)
		case <-closed:
		}
	}()
	// Returns once the last window is closed, or on Quit or Terminate.
	first.Run()
	for _, w := range all {
		w.Destroy()
	}
}
