//go:build cgo

package main

import (
	"context"
	"runtime"

	webview "github.com/webview/webview_go"
)

// The window is made and run on the main thread, as macOS insists.
func init() { runtime.LockOSThread() }

// window shows the page in a window of its own, the system's web view, until
// the window is closed or ctx ends.
func window(ctx context.Context, url string) {
	w := webview.New(false)
	defer w.Destroy()
	w.SetTitle("claude-mux")
	w.SetSize(1400, 900, webview.HintNone)
	nativeMenu()
	_ = w.Bind("cmuxNotify", notify)
	_ = w.Bind("cmuxOpen", openLink)
	_ = w.Bind("cmuxPaths", filePaths)
	_ = w.Bind("cmuxBadge", func(label string) { w.Dispatch(func() { badge(label) }) })
	w.Navigate(url)
	closed := make(chan struct{})
	defer close(closed)
	go func() {
		select {
		case <-ctx.Done():
			w.Dispatch(w.Terminate)
		case <-closed:
		}
	}()
	w.Run()
}
