//go:build cgo

package main

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework Cocoa -framework WebKit
#import <Cocoa/Cocoa.h>
#import <WebKit/WebKit.h>

// When the window closes, empty its web view, so its page unloads.
static void unloadOnClose(void *ptr) {
	NSWindow *window = (__bridge NSWindow *)ptr;
	__block id token = [[NSNotificationCenter defaultCenter]
		addObserverForName:NSWindowWillCloseNotification object:window queue:nil
		usingBlock:^(NSNotification *note) {
			NSView *view = window.contentView;
			if ([view isKindOfClass:[WKWebView class]]) {
				[(WKWebView *)view loadHTMLString:@"" baseURL:nil];
			}
			[[NSNotificationCenter defaultCenter] removeObserver:token];
		}];
}
*/
import "C"

import "unsafe"

// unloadOnClose makes a window's page unload when the window is closed. On
// a Mac, closing a window only hides it: webview frees its web view when the
// app quits, so the page would go on running unseen (its terminals, its
// stream from the hub, its notifications, its place in the window count).
func unloadOnClose(window unsafe.Pointer) { C.unloadOnClose(window) }
