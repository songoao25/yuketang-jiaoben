//go:build darwin && cgo

package main

// dockicon_darwin.go：用 Cocoa 设置 NSApplication 图标并读取当前外观。

/*
#cgo CFLAGS: -x objective-c -fblocks
#cgo LDFLAGS: -framework Cocoa
#import <Cocoa/Cocoa.h>
#import <dispatch/dispatch.h>

void yktSetDockIcon(const void *data, int len) {
	NSData *bytes = [NSData dataWithBytes:data length:(NSUInteger)len];
	dispatch_async(dispatch_get_main_queue(), ^{
		@autoreleasepool {
			NSImage *image = [[NSImage alloc] initWithData:bytes];
			if (image != nil) {
				[NSApp setApplicationIconImage:image];
			}
		}
	});
}

int yktIsDarkMode(void) {
	NSString *style = [[NSUserDefaults standardUserDefaults] stringForKey:@"AppleInterfaceStyle"];
	if (style == nil) {
		return 0;
	}
	return [style caseInsensitiveCompare:@"Dark"] == NSOrderedSame ? 1 : 0;
}
*/
import "C"

import "unsafe"

func setDockIconPNG(data []byte) {
	if len(data) == 0 {
		return
	}
	C.yktSetDockIcon(unsafe.Pointer(&data[0]), C.int(len(data)))
}

func systemIsDarkMode() bool { return C.yktIsDarkMode() != 0 }
