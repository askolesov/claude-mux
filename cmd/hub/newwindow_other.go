//go:build cgo && !darwin

package main

import (
	"fmt"
	"net/http"
	"os"

	webview "github.com/webview/webview_go"
)

// newWindow on Windows and Linux asks the hub to start a window process
// (POST /hub/windows): a third web view in one process stayed blank there
// until one of the first two closed.
func newWindow(_ webview.WebView, _ func(), url string) {
	res, err := http.Post(url+"/hub/windows", "", nil)
	if err == nil {
		_ = res.Body.Close()
		if res.StatusCode != http.StatusNoContent {
			err = fmt.Errorf("the hub answered %s", res.Status)
		}
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "claude-mux: new window:", err)
	}
}
