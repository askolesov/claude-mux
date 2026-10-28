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
	var all []webview.WebView // only touched on the main thread
	var open func()
	open = func() {
		w := webview.New(false)
		all = append(all, w)
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
