//go:build !cgo

package main

import "context"

// window, in a build without cgo, has no web view of its own: the page opens
// in the browser and the hub runs until interrupted.
func window(ctx context.Context, url string) {
	openBrowser(url)
	<-ctx.Done()
}
