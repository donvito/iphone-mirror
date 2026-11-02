// Cocoa window that shows the mirrored iPhone screen. Each frame is decoded by
// a VTDecompressionSession (hardware) and shown right away by putting its
// IOSurface into a CALayer. AVSampleBufferDisplayLayer was ~75-85 ms slower
// because it queues 4-5 frames before displaying them.
#import <Cocoa/Cocoa.h>
#import <CoreMedia/CoreMedia.h>
#import <QuartzCore/QuartzCore.h>
#import <VideoToolbox/VideoToolbox.h>
#include <mach/mach_time.h>

#include "ui_darwin.h"

enum { heldFrames = 3 }; // decoded frames kept alive so their surfaces aren't reused while on screen

static void onMain(dispatch_block_t b) {
	if (NSThread.isMainThread) {
		b();
	} else {
		dispatch_async(dispatch_get_main_queue(), b);
	}
}

static void logf_(NSString *fmt, ...) NS_FORMAT_FUNCTION(1, 2);
static void logf_(NSString *fmt, ...) {
	va_list ap;
	va_start(ap, fmt);
	NSString *s = [[NSString alloc] initWithFormat:fmt arguments:ap];
	va_end(ap);
	goLog((char *)s.UTF8String);
}

#pragma mark - Views

@interface VideoView : NSView
@property(readonly, strong) CALayer *videoLayer;
@end

@implementation VideoView
- (instancetype)initWithFrame:(NSRect)frame {
	if ((self = [super initWithFrame:frame])) {
		_videoLayer = [CALayer layer];
		_videoLayer.contentsGravity = kCAGravityResizeAspect; // letterbox
		_videoLayer.backgroundColor = NSColor.blackColor.CGColor;
		_videoLayer.frame = self.bounds;
		// Layer-hosting view: we own the layer and update it from the decoder thread.
		self.layer = _videoLayer;
		self.wantsLayer = YES;
	}
	return self;
}
- (void)setFrameSize:(NSSize)size {
	[super setFrameSize:size];
	[CATransaction begin];
	[CATransaction setDisableActions:YES];
	_videoLayer.frame = self.bounds;
	[CATransaction commit];
}
- (NSView *)hitTest:(NSPoint)p {
	return nil;
}
@end

@interface StatusView : NSView
@property(nonatomic, copy) NSString *text;
@end

@implementation StatusView
- (instancetype)initWithFrame:(NSRect)frame {
	if ((self = [super initWithFrame:frame])) {
		self.layerContentsRedrawPolicy = NSViewLayerContentsRedrawDuringViewResize;
	}
	return self;
}
- (void)setText:(NSString *)text {
	_text = [text copy];
	self.needsDisplay = YES;
}
- (NSView *)hitTest:(NSPoint)p {
	return nil;
}
- (void)drawRect:(NSRect)dirty {
	if (self.text.length == 0) {
		return;
	}
	NSMutableParagraphStyle *ps = [NSMutableParagraphStyle new];
	ps.alignment = NSTextAlignmentCenter;
	NSDictionary *attrs = @{
		NSFontAttributeName : [NSFont systemFontOfSize:15],
		NSForegroundColorAttributeName : [NSColor colorWithWhite:0.82 alpha:1],
		NSParagraphStyleAttributeName : ps,
	};
	NSRect box = NSInsetRect(self.bounds, 24, 24);
	NSRect m = [self.text boundingRectWithSize:NSMakeSize(box.size.width, CGFLOAT_MAX)
	                                   options:NSStringDrawingUsesLineFragmentOrigin
	                                attributes:attrs];
	CGFloat h = ceil(m.size.height);
	NSRect r = NSMakeRect(box.origin.x, round(NSMidY(self.bounds) - h / 2), box.size.width, h);
	[self.text drawWithRect:r options:NSStringDrawingUsesLineFragmentOrigin attributes:attrs];
}
@end

@interface FlippedView : NSView
@end
@implementation FlippedView
- (BOOL)isFlipped {
	return YES;
}
@end

@class Controller;

@interface MirrorView : NSView
@property(weak) Controller *controller;
@end

#pragma mark - Controller

@interface Controller : NSObject <NSApplicationDelegate, NSWindowDelegate> {
  @public
	// Only touched by ui_video_* calls, which the Go side serializes.
	CMVideoFormatDescriptionRef fmt;
	VTDecompressionSessionRef session;
	int decodeErrors;
	// Guarded by @synchronized(self); also used by the decoder callback and the main thread.
	int videoW, videoH;
	BOOL hasFrame;
	CVPixelBufferRef held[heldFrames];
	int heldNext;
	int shownW, shownH;
	double decodeSum, decodeMax;
	int decoded;
}
@property(strong) NSWindow *window;
@property(strong) VideoView *videoView;
@property(strong) StatusView *statusView;
@property(strong) NSMutableArray<NSMenuItem *> *onTopItems;
@property(copy) NSString *logsDir;
@property BOOL onTop;

@property(strong) NSWindow *settingsWindow;
@property(strong) NSTextField *nameField;
@property(strong) NSPopUpButton *qualityPopup;
@property(strong) NSPopUpButton *fpsPopup;
@property(strong) NSButton *statusCheck;
@property(strong) NSButton *onTopCheck;
- (void)fitToVideo:(id)sender;
@end

static Controller *C_;
static id<NSObject> activity;

@implementation MirrorView
- (BOOL)acceptsFirstMouse:(NSEvent *)e {
	return YES;
}
- (void)mouseDown:(NSEvent *)e {
	if (e.clickCount == 2) {
		[self.controller fitToVideo:nil];
	}
}
@end

@implementation Controller

- (NSMenuItem *)item:(NSString *)title action:(SEL)action key:(NSString *)key {
	NSMenuItem *it = [[NSMenuItem alloc] initWithTitle:title action:action keyEquivalent:key];
	if ([self respondsToSelector:action]) {
		it.target = self;
	}
	return it;
}

- (NSMenuItem *)submenu:(NSMenu *)menu {
	NSMenuItem *it = [[NSMenuItem alloc] initWithTitle:menu.title action:nil keyEquivalent:@""];
	it.submenu = menu;
	return it;
}

- (NSMenuItem *)onTopItem:(NSString *)key {
	NSMenuItem *it = [self item:@"Keep Window on Top" action:@selector(toggleOnTop:) key:key];
	it.state = self.onTop ? NSControlStateValueOn : NSControlStateValueOff;
	[self.onTopItems addObject:it];
	return it;
}

- (void)buildMenus:(NSString *)title {
	NSMenu *bar = [NSMenu new];

	NSMenu *app = [[NSMenu alloc] initWithTitle:title];
	[app addItem:[self item:[@"About " stringByAppendingString:title] action:@selector(orderFrontStandardAboutPanel:) key:@""]];
	[app addItem:NSMenuItem.separatorItem];
	[app addItem:[self item:@"Settings…" action:@selector(openSettings:) key:@","]];
	[app addItem:NSMenuItem.separatorItem];
	[app addItem:[self item:[@"Hide " stringByAppendingString:title] action:@selector(hide:) key:@"h"]];
	NSMenuItem *others = [self item:@"Hide Others" action:@selector(hideOtherApplications:) key:@"h"];
	others.keyEquivalentModifierMask = NSEventModifierFlagOption | NSEventModifierFlagCommand;
	[app addItem:others];
	[app addItem:[self item:@"Show All" action:@selector(unhideAllApplications:) key:@""]];
	[app addItem:NSMenuItem.separatorItem];
	[app addItem:[self item:[@"Quit " stringByAppendingString:title] action:@selector(terminate:) key:@"q"]];
	[bar addItem:[self submenu:app]];

	NSMenu *win = [[NSMenu alloc] initWithTitle:@"Window"];
	[win addItem:[self item:@"Minimize" action:@selector(performMiniaturize:) key:@"m"]];
	[win addItem:[self item:@"Zoom" action:@selector(performZoom:) key:@""]];
	[win addItem:NSMenuItem.separatorItem];
	[win addItem:[self item:@"Fit Window to Phone" action:@selector(fitToVideo:) key:@"0"]];
	[win addItem:[self onTopItem:@"t"]];
	[win addItem:NSMenuItem.separatorItem];
	[win addItem:[self item:@"Open Log Folder" action:@selector(openLogs:) key:@""]];
	[bar addItem:[self submenu:win]];

	NSApp.mainMenu = bar;
	NSApp.windowsMenu = win;
}

- (NSMenu *)contextMenu {
	NSMenu *m = [NSMenu new];
	[m addItem:[self item:@"Settings…" action:@selector(openSettings:) key:@""]];
	[m addItem:[self item:@"Fit Window to Phone" action:@selector(fitToVideo:) key:@""]];
	[m addItem:[self onTopItem:@""]];
	[m addItem:[self item:@"Open Log Folder" action:@selector(openLogs:) key:@""]];
	[m addItem:NSMenuItem.separatorItem];
	[m addItem:[self item:@"Quit" action:@selector(terminate:) key:@""]];
	return m;
}

- (void)createWindow:(NSString *)title {
	self.window = [[NSWindow alloc] initWithContentRect:NSMakeRect(0, 0, 400, 860)
	                                          styleMask:NSWindowStyleMaskTitled | NSWindowStyleMaskClosable |
	                                                    NSWindowStyleMaskMiniaturizable | NSWindowStyleMaskResizable
	                                            backing:NSBackingStoreBuffered
	                                              defer:NO];
	self.window.title = title; // OBS finds the window by this title
	self.window.backgroundColor = NSColor.blackColor;
	self.window.releasedWhenClosed = NO;
	self.window.delegate = self;
	self.window.contentMinSize = NSMakeSize(160, 160);
	self.window.collectionBehavior = NSWindowCollectionBehaviorFullScreenPrimary;

	MirrorView *root = [[MirrorView alloc] initWithFrame:NSMakeRect(0, 0, 400, 860)];
	root.controller = self;
	root.wantsLayer = YES;
	root.layer.backgroundColor = NSColor.blackColor.CGColor;
	root.menu = [self contextMenu];

	self.videoView = [[VideoView alloc] initWithFrame:root.bounds];
	self.videoView.autoresizingMask = NSViewWidthSizable | NSViewHeightSizable;
	[root addSubview:self.videoView];

	self.statusView = [[StatusView alloc] initWithFrame:root.bounds];
	self.statusView.autoresizingMask = NSViewWidthSizable | NSViewHeightSizable;
	[root addSubview:self.statusView];

	self.window.contentView = root;
	self.window.level = self.onTop ? NSFloatingWindowLevel : NSNormalWindowLevel;
	[self fitToWidth:1290 height:2796]; // iPhone 15 Pro Max portrait
}

// fitToWidth makes the window as tall as the screen allows with a content area
// of the given aspect ratio. OBS captures the window at its on-screen size, so bigger is sharper.
- (void)fitToWidth:(CGFloat)aw height:(CGFloat)ah {
	if (aw <= 0 || ah <= 0) {
		return;
	}
	NSScreen *screen = self.window.screen ?: NSScreen.mainScreen;
	NSRect vis = screen.visibleFrame;
	NSRect probe = [self.window frameRectForContentRect:NSMakeRect(0, 0, 100, 100)];
	CGFloat chromeW = probe.size.width - 100, chromeH = probe.size.height - 100;
	CGFloat ch = vis.size.height - chromeH, cw = ch * aw / ah;
	if (cw > vis.size.width - chromeW) {
		cw = vis.size.width - chromeW;
		ch = cw * ah / aw;
	}
	cw = round(cw), ch = round(ch);
	NSRect f = self.window.frame;
	CGFloat x = f.origin.x;
	if (x < NSMinX(vis) || x + cw + chromeW > NSMaxX(vis)) {
		x = round(NSMinX(vis) + (vis.size.width - cw - chromeW) / 2);
	}
	[self.window setFrame:NSMakeRect(x, NSMaxY(vis) - ch - chromeH, cw + chromeW, ch + chromeH) display:YES];
}

- (void)fitToVideo:(id)sender {
	int w, h;
	@synchronized(self) {
		w = videoW, h = videoH;
	}
	if (w == 0 || h == 0) {
		w = 1290, h = 2796;
	}
	[self fitToWidth:w height:h];
}

- (void)setOnTopState:(BOOL)on {
	self.onTop = on;
	self.window.level = on ? NSFloatingWindowLevel : NSNormalWindowLevel;
	for (NSMenuItem *it in self.onTopItems) {
		it.state = on ? NSControlStateValueOn : NSControlStateValueOff;
	}
}

- (void)toggleOnTop:(id)sender {
	[self setOnTopState:!self.onTop];
	// Persist through the settings, like the checkbox in the settings window.
	goApplySettings(NULL, -1, -1, -1, self.onTop);
}

- (void)openLogs:(id)sender {
	if (self.logsDir.length) {
		[NSWorkspace.sharedWorkspace openURL:[NSURL fileURLWithPath:self.logsDir isDirectory:YES]];
	}
}

- (void)openSettings:(id)sender {
	if (self.settingsWindow.visible) {
		[self.settingsWindow makeKeyAndOrderFront:nil];
		return;
	}
	goOpenSettings(); // calls back into showSettings...
}

#pragma mark Settings window

- (NSTextField *)label:(NSString *)s at:(CGFloat)y in:(NSView *)v {
	NSTextField *l = [NSTextField labelWithString:s];
	l.frame = NSMakeRect(20, y, 400, 18);
	[v addSubview:l];
	return l;
}

- (void)showSettingsName:(NSString *)name hevc:(BOOL)hevc fps:(int)fps status:(BOOL)status onTop:(BOOL)onTop {
	NSWindow *w = [[NSWindow alloc] initWithContentRect:NSMakeRect(0, 0, 440, 336)
	                                          styleMask:NSWindowStyleMaskTitled | NSWindowStyleMaskClosable
	                                            backing:NSBackingStoreBuffered
	                                              defer:NO];
	w.title = @"iPhone Mirror Settings";
	w.releasedWhenClosed = NO;
	FlippedView *v = [[FlippedView alloc] initWithFrame:NSMakeRect(0, 0, 440, 336)];
	w.contentView = v;

	[self label:@"Name shown on the iPhone" at:16 in:v];
	self.nameField = [NSTextField textFieldWithString:name];
	self.nameField.frame = NSMakeRect(20, 38, 400, 24);
	[v addSubview:self.nameField];

	[self label:@"Quality" at:76 in:v];
	self.qualityPopup = [[NSPopUpButton alloc] initWithFrame:NSMakeRect(17, 96, 406, 26) pullsDown:NO];
	[self.qualityPopup addItemsWithTitles:@[ @"High (HEVC, up to 2160p)", @"Compatible (H.264, up to 1080p, less CPU)" ]];
	[self.qualityPopup selectItemAtIndex:hevc ? 0 : 1];
	[v addSubview:self.qualityPopup];

	[self label:@"Maximum frame rate" at:136 in:v];
	self.fpsPopup = [[NSPopUpButton alloc] initWithFrame:NSMakeRect(17, 156, 406, 26) pullsDown:NO];
	[self.fpsPopup addItemsWithTitles:@[ @"60 fps", @"30 fps" ]];
	[self.fpsPopup selectItemAtIndex:fps == 30 ? 1 : 0];
	[v addSubview:self.fpsPopup];

	self.statusCheck = [NSButton checkboxWithTitle:@"Show instructions while no iPhone is connected" target:nil action:nil];
	self.statusCheck.frame = NSMakeRect(20, 198, 400, 20);
	self.statusCheck.state = status ? NSControlStateValueOn : NSControlStateValueOff;
	[v addSubview:self.statusCheck];

	self.onTopCheck = [NSButton checkboxWithTitle:@"Keep window on top of other windows" target:nil action:nil];
	self.onTopCheck.frame = NSMakeRect(20, 224, 400, 20);
	self.onTopCheck.state = onTop ? NSControlStateValueOn : NSControlStateValueOff;
	[v addSubview:self.onTopCheck];

	NSTextField *note = [self label:@"Changing the name, quality or frame rate briefly disconnects the iPhone." at:258 in:v];
	note.font = [NSFont systemFontOfSize:NSFont.smallSystemFontSize];
	note.textColor = NSColor.secondaryLabelColor;

	NSButton *cancel = [NSButton buttonWithTitle:@"Cancel" target:self action:@selector(cancelSettings:)];
	cancel.frame = NSMakeRect(232, 290, 92, 28);
	cancel.keyEquivalent = @"\033";
	[v addSubview:cancel];
	NSButton *save = [NSButton buttonWithTitle:@"Save" target:self action:@selector(saveSettings:)];
	save.frame = NSMakeRect(328, 290, 92, 28);
	save.keyEquivalent = @"\r";
	[v addSubview:save];

	self.settingsWindow = w;
	NSRect mf = self.window.frame;
	[w setFrameTopLeftPoint:NSMakePoint(NSMidX(mf) - w.frame.size.width / 2, NSMaxY(mf) - 80)];
	[w makeKeyAndOrderFront:nil];
	[w makeFirstResponder:self.nameField];
	[NSApp activate];
}

- (void)saveSettings:(id)sender {
	NSString *name = self.nameField.stringValue ?: @"";
	goApplySettings((char *)name.UTF8String, self.qualityPopup.indexOfSelectedItem == 0,
	                self.fpsPopup.indexOfSelectedItem == 1 ? 30 : 60, self.statusCheck.state == NSControlStateValueOn,
	                self.onTopCheck.state == NSControlStateValueOn);
	[self.settingsWindow close];
}

- (void)cancelSettings:(id)sender {
	[self.settingsWindow close];
}

#pragma mark App / window delegate

- (void)applicationDidFinishLaunching:(NSNotification *)n {
	[self.window makeKeyAndOrderFront:nil];
	[NSApp activate];
}

- (void)applicationWillTerminate:(NSNotification *)n {
	goWillTerminate();
}

- (void)windowWillClose:(NSNotification *)n {
	if (n.object == self.window) {
		[NSApp terminate:nil];
	}
}

- (BOOL)applicationSupportsSecureRestorableState:(NSApplication *)app {
	return YES;
}

@end

#pragma mark - C API

void ui_init(const char *title) {
	NSString *t = [NSString stringWithUTF8String:title];
	[NSApplication sharedApplication];
	NSApp.activationPolicy = NSApplicationActivationPolicyRegular;
	C_ = [Controller new];
	C_.onTopItems = [NSMutableArray array];
	NSApp.delegate = C_;
	[C_ buildMenus:t];
	[C_ createWindow:t];
	// Keep App Nap from throttling the receiver while the window is hidden behind others (e.g. for OBS).
	activity = [NSProcessInfo.processInfo beginActivityWithOptions:NSActivityUserInitiatedAllowingIdleSystemSleep |
	                                                               NSActivityLatencyCritical
	                                                        reason:@"Receiving AirPlay screen mirroring"];
}

void ui_run(void) {
	[NSApp run];
}

void ui_set_logs_dir(const char *dir) {
	NSString *d = [NSString stringWithUTF8String:dir];
	onMain(^{ C_.logsDir = d; });
}

void ui_set_status(const char *text) {
	NSString *s = [NSString stringWithUTF8String:text];
	onMain(^{ C_.statusView.text = s; });
}

void ui_set_on_top(int on) {
	onMain(^{ [C_ setOnTopState:on != 0]; });
}

void ui_show_settings(const char *name, int hevc, int fps, int showStatus, int onTop) {
	NSString *n = [NSString stringWithUTF8String:name];
	onMain(^{ [C_ showSettingsName:n hevc:hevc != 0 fps:fps status:showStatus != 0 onTop:onTop != 0]; });
}

static double msSince(uint64_t t0) {
	static mach_timebase_info_data_t tb;
	if (tb.denom == 0) {
		mach_timebase_info(&tb);
	}
	return (double)(mach_absolute_time() - t0) * tb.numer / tb.denom / 1e6;
}

static void showSurface(Controller *c, IOSurfaceRef s) {
	[CATransaction begin];
	[CATransaction setDisableActions:YES];
	c.videoView.videoLayer.contents = (__bridge id)s;
	[CATransaction commit];
}

// decoded runs on a VideoToolbox thread for each decoded frame and puts it on screen immediately.
static void decoded(void *refcon, void *frameRefcon, OSStatus st, VTDecodeInfoFlags flags, CVImageBufferRef img,
                    CMTime pts, CMTime dur) {
	Controller *c = C_;
	IOSurfaceRef s = img ? CVPixelBufferGetIOSurface(img) : NULL;
	if (st != noErr || !s || (flags & kVTDecodeInfo_FrameDropped)) {
		return;
	}
	double ms = msSince((uint64_t)(uintptr_t)frameRefcon);
	CVPixelBufferRetain(img);
	CVPixelBufferRef old;
	BOOL first;
	@synchronized(c) {
		old = c->held[c->heldNext];
		c->held[c->heldNext] = img;
		c->heldNext = (c->heldNext + 1) % heldFrames;
		first = !c->hasFrame;
		c->hasFrame = YES;
		c->shownW = (int)CVPixelBufferGetWidth(img), c->shownH = (int)CVPixelBufferGetHeight(img);
		c->decodeSum += ms, c->decoded++;
		c->decodeMax = MAX(c->decodeMax, ms);
	}
	showSurface(c, s);
	if (old) {
		CVPixelBufferRelease(old);
	}
	if (first) {
		onMain(^{ c.statusView.hidden = YES; });
	}
}

static OSStatus createSession(Controller *c) {
	NSDictionary *spec = @{(id)kVTVideoDecoderSpecification_EnableHardwareAcceleratedVideoDecoder : @YES};
	NSDictionary *attrs = @{
		(id)kCVPixelBufferPixelFormatTypeKey : @(kCVPixelFormatType_32BGRA),
		(id)kCVPixelBufferIOSurfacePropertiesKey : @{},
	};
	VTDecompressionOutputCallbackRecord cb = {decoded, NULL};
	OSStatus st = VTDecompressionSessionCreate(NULL, c->fmt, (__bridge CFDictionaryRef)spec,
	                                           (__bridge CFDictionaryRef)attrs, &cb, &c->session);
	if (st == noErr) {
		VTSessionSetProperty(c->session, kVTDecompressionPropertyKey_RealTime, kCFBooleanTrue);
	} else {
		c->session = NULL;
	}
	c->decodeErrors = 0;
	return st;
}

static void destroySession(Controller *c) {
	if (c->session) {
		VTDecompressionSessionWaitForAsynchronousFrames(c->session);
		VTDecompressionSessionInvalidate(c->session);
		CFRelease(c->session);
		c->session = NULL;
	}
}

int ui_video_config(int hevc, const uint8_t *ps, const size_t *sizes, int count, int width, int height) {
	enum { maxSets = 16 };
	const uint8_t *ptrs[maxSets];
	size_t lens[maxSets];
	if (count > maxSets) {
		count = maxSets;
	}
	size_t off = 0;
	for (int i = 0; i < count; i++) {
		ptrs[i] = ps + off;
		lens[i] = sizes[i];
		off += sizes[i];
	}
	CMVideoFormatDescriptionRef f = NULL;
	OSStatus st = hevc ? CMVideoFormatDescriptionCreateFromHEVCParameterSets(NULL, count, ptrs, lens, 4, NULL, &f)
	                   : CMVideoFormatDescriptionCreateFromH264ParameterSets(NULL, count, ptrs, lens, 4, &f);
	if (st != noErr) {
		return st;
	}
	Controller *c = C_;
	if (c->fmt && CMFormatDescriptionEqual(c->fmt, f)) {
		CFRelease(f);
		return 0;
	}
	// New stream (e.g. the phone was rotated): start a fresh decoder.
	destroySession(c);
	if (c->fmt) {
		CFRelease(c->fmt);
	}
	c->fmt = f;
	@synchronized(c) {
		c->videoW = width, c->videoH = height;
	}
	return createSession(c);
}

void ui_video_frame(const uint8_t *data, size_t len) {
	Controller *c = C_;
	if (!c->fmt || (!c->session && createSession(c) != noErr)) {
		return;
	}
	CMBlockBufferRef bb = NULL;
	CMSampleBufferRef sb = NULL;
	if (CMBlockBufferCreateWithMemoryBlock(NULL, NULL, len, NULL, NULL, 0, len, kCMBlockBufferAssureMemoryNowFlag, &bb) ==
	        noErr &&
	    CMBlockBufferReplaceDataBytes(data, bb, 0, len) == noErr) {
		CMSampleBufferCreateReady(NULL, bb, c->fmt, 1, 0, NULL, 1, &len, &sb);
	}
	if (bb) {
		CFRelease(bb);
	}
	if (!sb) {
		return;
	}
	// Synchronous decode: the frame is on screen (via decoded) by the time this returns.
	VTDecodeInfoFlags info = 0;
	OSStatus st = VTDecompressionSessionDecodeFrame(c->session, sb, kVTDecodeFrame_1xRealTimePlayback,
	                                                (void *)(uintptr_t)mach_absolute_time(), &info);
	CFRelease(sb);
	if (st == kVTInvalidSessionErr) { // e.g. after sleep/wake; frames resume at the next keyframe
		logf_(@"video: decoder session lost, recreating");
		destroySession(c);
		createSession(c);
	} else if (st != noErr && c->decodeErrors++ < 3) {
		logf_(@"video: decode error %d", (int)st);
	}
}

int ui_video_stats(int *width, int *height, double *avgMs, double *maxMs) {
	Controller *c = C_;
	@synchronized(c) {
		int n = c->decoded;
		*width = c->shownW, *height = c->shownH;
		*avgMs = n ? c->decodeSum / n : 0;
		*maxMs = c->decodeMax;
		c->decodeSum = c->decodeMax = 0, c->decoded = 0;
		return n;
	}
}

void ui_video_clear(void) {
	Controller *c = C_;
	destroySession(c);
	if (c->fmt) {
		CFRelease(c->fmt);
		c->fmt = NULL;
	}
	showSurface(c, NULL);
	CVPixelBufferRef old[heldFrames];
	@synchronized(c) {
		c->hasFrame = NO;
		c->decodeSum = c->decodeMax = 0, c->decoded = 0;
		for (int i = 0; i < heldFrames; i++) {
			old[i] = c->held[i];
			c->held[i] = NULL;
		}
	}
	for (int i = 0; i < heldFrames; i++) {
		if (old[i]) {
			CVPixelBufferRelease(old[i]);
		}
	}
	onMain(^{ c.statusView.hidden = NO; });
}
