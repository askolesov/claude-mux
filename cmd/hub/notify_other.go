//go:build cgo && !darwin

package main

// notify: the Windows and Linux web views have the page's own notifications.
func notify(title, body string) {}
