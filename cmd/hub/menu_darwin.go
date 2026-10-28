//go:build cgo

package main

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework Cocoa
#import <Cocoa/Cocoa.h>
#include <stdlib.h>

static NSMenuItem *item(NSString *title, SEL action, NSString *key) {
	return [[NSMenuItem alloc] initWithTitle:title action:action keyEquivalent:key];
}

static void add(NSMenu *bar, NSMenu *menu) {
	NSMenuItem *top = [NSMenuItem new];
	top.submenu = menu;
	[bar addItem:top];
}

static void nativeMenu(void) {
	NSMenu *app = [[NSMenu alloc] initWithTitle:@"claude-mux"];
	[app addItem:item(@"Hide claude-mux", @selector(hide:), @"h")];
	[app addItem:[NSMenuItem separatorItem]];
	// Quit stops the app's run loop, with every window, so the hub shuts its
	// backends down on the way out.
	[app addItem:item(@"Quit claude-mux", @selector(stop:), @"q")];

	NSMenu *edit = [[NSMenu alloc] initWithTitle:@"Edit"];
	[edit addItem:item(@"Undo", @selector(undo:), @"z")];
	NSMenuItem *redo = item(@"Redo", @selector(redo:), @"z");
	redo.keyEquivalentModifierMask = NSEventModifierFlagCommand | NSEventModifierFlagShift;
	[edit addItem:redo];
	[edit addItem:[NSMenuItem separatorItem]];
	[edit addItem:item(@"Cut", @selector(cut:), @"x")];
	[edit addItem:item(@"Copy", @selector(copy:), @"c")];
	[edit addItem:item(@"Paste", @selector(paste:), @"v")];
	[edit addItem:item(@"Select All", @selector(selectAll:), @"a")];

	// The web view answers reload: itself.
	NSMenu *view = [[NSMenu alloc] initWithTitle:@"View"];
	[view addItem:item(@"Reload", @selector(reload:), @"r")];

	NSMenu *win = [[NSMenu alloc] initWithTitle:@"Window"];
	[win addItem:item(@"Minimize", @selector(performMiniaturize:), @"m")];
	// No ⌘W or ⌘N here: the page takes them, to close a tab and to open a
	// window.
	[win addItem:item(@"Close Window", @selector(performClose:), @"")];

	NSMenu *bar = [NSMenu new];
	add(bar, app);
	add(bar, edit);
	add(bar, view);
	add(bar, win);
	[NSApp setMainMenu:bar];
	[NSApp setWindowsMenu:win];
}
static void setBadge(const char *label) {
	[[NSApp dockTile] setBadgeLabel:(label[0] ? [NSString stringWithUTF8String:label] : nil)];
}
*/
import "C"

import "unsafe"

// nativeMenu gives the app the menus a Mac app has: without an Edit menu a
// web view takes no ⌘C or ⌘V.
func nativeMenu() { C.nativeMenu() }

// badge sets the Dock icon's badge: the count of tabs that wait, or ""
// for none. Run it on the main thread.
func badge(label string) {
	s := C.CString(label)
	defer C.free(unsafe.Pointer(s))
	C.setBadge(s)
}
