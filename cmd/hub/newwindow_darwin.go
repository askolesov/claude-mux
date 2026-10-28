//go:build cgo

package main

import webview "github.com/webview/webview_go"

// newWindow on a Mac makes another web view in this process: one app, one
// Dock icon, and WebKit draws any number of them.
func newWindow(w webview.WebView, open func(), _ string) { w.Dispatch(open) }
