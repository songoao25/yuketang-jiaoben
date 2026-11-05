#import <Foundation/Foundation.h>
#import <AppKit/AppKit.h>
#import <objc/message.h>
#import <dlfcn.h>
#include <string.h>
#include <dispatch/dispatch.h>

static id gUpdaterController = nil;
static id gUpdater = nil;
static char gLastError[1024];

static void cxSetError(NSString *message) {
	snprintf(gLastError, sizeof(gLastError), "%s", [message UTF8String] ?: "");
}

static void cxInstallMenuItem(void) {
	NSMenu *mainMenu = [NSApp mainMenu];
	if (mainMenu == nil || mainMenu.numberOfItems == 0) {
		return;
	}
	NSMenuItem *appMenuItem = [mainMenu itemAtIndex:0];
	NSMenu *appMenu = [appMenuItem submenu];
	if (appMenu == nil) {
		return;
	}
	for (NSMenuItem *item in [appMenu.itemArray copy]) {
		if ([item action] == @selector(checkForUpdates:)) {
			[appMenu removeItem:item];
		}
	}
	NSMenuItem *checkItem = [[NSMenuItem alloc] initWithTitle:@"检查更新…" action:@selector(checkForUpdates:) keyEquivalent:@""];
	checkItem.target = gUpdaterController;
	NSUInteger index = appMenu.numberOfItems > 1 ? 1 : appMenu.numberOfItems;
	[appMenu insertItem:checkItem atIndex:index];
}

static int cxInitUpdater(void) {
	@autoreleasepool {
		if (gUpdaterController != nil && gUpdater != nil) {
			return 1;
		}
		NSString *bundlePath = [[NSBundle mainBundle] bundlePath];
		NSString *frameworkPath = [bundlePath stringByAppendingPathComponent:@"Contents/Frameworks/Sparkle.framework/Sparkle"];
		void *handle = dlopen([frameworkPath fileSystemRepresentation], RTLD_NOW | RTLD_LOCAL);
		if (handle == NULL) {
			const char *reason = dlerror();
			cxSetError([NSString stringWithFormat:@"无法加载 Sparkle 框架：%s", reason ? reason : "未知错误"]);
			return 0;
		}
		Class controllerClass = NSClassFromString(@"SPUStandardUpdaterController");
		if (controllerClass == Nil) {
			cxSetError(@"Sparkle 框架缺少 SPUStandardUpdaterController");
			return 0;
		}
		SEL initSelector = NSSelectorFromString(@"initWithStartingUpdater:updaterDelegate:userDriverDelegate:");
		if (![controllerClass instancesRespondToSelector:initSelector]) {
			cxSetError(@"Sparkle 版本不支持 SPUStandardUpdaterController");
			return 0;
		}
		id (*initMessage)(id, SEL, BOOL, id, id) = (void *)objc_msgSend;
		gUpdaterController = initMessage([controllerClass alloc], initSelector, YES, nil, nil);
		if (gUpdaterController == nil) {
			cxSetError(@"Sparkle Updater 创建失败");
			return 0;
		}
		SEL updaterSelector = NSSelectorFromString(@"updater");
		if ([gUpdaterController respondsToSelector:updaterSelector]) {
			id (*getUpdater)(id, SEL) = (void *)objc_msgSend;
			gUpdater = getUpdater(gUpdaterController, updaterSelector);
		}
		if (gUpdater == nil) {
			cxSetError(@"无法获取 Sparkle Updater 实例");
			return 0;
		}
		cxInstallMenuItem();
		return 1;
	}
}

int cxSparkleInit(void) {
	if ([NSThread isMainThread]) {
		return cxInitUpdater();
	}
	__block int result = 0;
	dispatch_sync(dispatch_get_main_queue(), ^{
		result = cxInitUpdater();
	});
	return result;
}

int cxSparkleCheck(void) {
	if (!cxSparkleInit()) {
		return 0;
	}
	id updater = gUpdater;
	dispatch_async(dispatch_get_main_queue(), ^{
		@autoreleasepool {
			[updater performSelector:@selector(checkForUpdates:) withObject:nil];
		}
	});
	return 1;
}

const char *cxSparkleLastError(void) {
	return gLastError;
}
