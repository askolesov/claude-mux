//go:build cgo && !darwin

package main

import "unsafe"

// nativeMenu: Windows and Linux web views copy and paste without one.
func nativeMenu() {}

// badge: the taskbar has no count to set from here.
func badge(string) {}

// unloadOnClose: WebKitGTK destroys a window's web view with the window,
// and WebView2 its page with its parent window.
func unloadOnClose(unsafe.Pointer) {}
