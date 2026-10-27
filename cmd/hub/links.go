package main

import (
	"fmt"
	"net/url"

	"github.com/askolesov/claude-mux/internal/sysproc"
)

// openLink opens a link clicked in a terminal in the default browser: the
// window's web view opens no windows of its own. Only http and https, since
// the system's opener would also start programs and open files.
func openLink(link string) error {
	u, err := url.Parse(link)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("not a web link: %q", link)
	}
	return sysproc.Open(u.String())
}
