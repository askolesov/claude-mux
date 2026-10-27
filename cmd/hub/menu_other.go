//go:build cgo && !darwin

package main

// nativeMenu: Windows and Linux web views copy and paste without one.
func nativeMenu() {}

// badge: the taskbar has no count to set from here.
func badge(string) {}
