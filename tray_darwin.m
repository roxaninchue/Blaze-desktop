#import <Cocoa/Cocoa.h>
#import <ApplicationServices/ApplicationServices.h>
#import <objc/runtime.h>

extern void blazeTrayStart(void);
extern void blazeReopen(void);

// Clicking the Dock icon while Blaze is hidden in the menu bar brings the window back.
// Wails' app delegate has no reopen handler, so add one to its class.
static BOOL blazeShouldHandleReopen(id self, SEL _cmd, NSApplication *app, BOOL hasVisibleWindows) {
	if (!hasVisibleWindows) {
		blazeReopen();
	}
	return YES;
}

// Runs systray's start function on the main thread, where AppKit wants it.
void blazeOnMain(void) {
	dispatch_async(dispatch_get_main_queue(), ^{
		id delegate = [NSApp delegate];
		if (delegate != nil) {
			class_addMethod([delegate class], @selector(applicationShouldHandleReopen:hasVisibleWindows:), (IMP)blazeShouldHandleReopen, "c@:@c");
		}
		blazeTrayStart();
	});
}

void blazeAskAccessibility(void) {
	NSDictionary *opts = @{(id)kAXTrustedCheckOptionPrompt: @YES};
	AXIsProcessTrustedWithOptions((CFDictionaryRef)opts);
}
