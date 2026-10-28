//go:build cgo

package main

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework Cocoa
#import <Cocoa/Cocoa.h>
#include <stdlib.h>
#include <string.h>

// The paths of the files on a pasteboard, one per line.
static char *pasteboardPaths(int drag) {
	@autoreleasepool {
		NSPasteboard *pb = drag ? [NSPasteboard pasteboardWithName:NSPasteboardNameDrag] : [NSPasteboard generalPasteboard];
		NSArray<NSURL *> *urls = [pb readObjectsForClasses:@[[NSURL class]]
			options:@{NSPasteboardURLReadingFileURLsOnlyKey: @YES}];
		NSMutableArray<NSString *> *paths = [NSMutableArray array];
		for (NSURL *u in urls) {
			[paths addObject:u.path];
		}
		return strdup([[paths componentsJoinedByString:@"\n"] UTF8String]);
	}
}
*/
import "C"

import (
	"strings"
	"unsafe"
)

// filePaths gives the page the paths of the files just dropped on the
// window (from "drag") or copied in Finder ("clipboard"), which a web page
// never learns: a local tab then gets the files where they are.
func filePaths(from string) []string {
	s := C.pasteboardPaths(C.int(map[string]int{"drag": 1}[from]))
	defer C.free(unsafe.Pointer(s))
	if C.GoString(s) == "" {
		return []string{}
	}
	return strings.Split(C.GoString(s), "\n")
}
