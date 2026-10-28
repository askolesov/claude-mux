//go:build cgo && !darwin

package main

// filePaths: on Windows and Linux the page copies dropped files instead.
func filePaths(string) []string { return []string{} }
