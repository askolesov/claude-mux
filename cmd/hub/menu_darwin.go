//go:build cgo

package main

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework Cocoa
#import <Cocoa/Cocoa.h>

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
	// Quit closes the window, so the hub shuts its backends down on the way out.
	[app addItem:item(@"Quit claude-mux", @selector(performClose:), @"q")];

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
	[win addItem:item(@"Close", @selector(performClose:), @"w")];

	NSMenu *bar = [NSMenu new];
	add(bar, app);
	add(bar, edit);
	add(bar, view);
	add(bar, win);
	[NSApp setMainMenu:bar];
	[NSApp setWindowsMenu:win];
}
*/
import "C"

// nativeMenu gives the app the menus a Mac app has: without an Edit menu a
// web view takes no ⌘C or ⌘V.
func nativeMenu() { C.nativeMenu() }
