//go:build !cgo

package main

import (
	"context"

	"github.com/askolesov/claude-mux/internal/sysproc"
)

// window, in a build without cgo, has no web view of its own: the page opens
// in the browser and the hub runs until interrupted.
func window(ctx context.Context, url string) {
	_ = sysproc.Open(url)
	<-ctx.Done()
}
