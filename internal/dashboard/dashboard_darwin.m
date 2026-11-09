#import <Cocoa/Cocoa.h>
#import <WebKit/WebKit.h>
#import <AVKit/AVKit.h>
#import <AVFoundation/AVFoundation.h>
#import "dashboard.h"
#import <objc/runtime.h>
extern void goDashboardMessage(char *raw);
extern void goDashboardReady(void);

@interface TalaWebView : WKWebView
@end
// A native drag surface keeps the integrated header movable without swallowing web controls.
@interface TalaDragView : NSView
@end
@implementation TalaDragView
- (void)mouseDown:(NSEvent *)event {
 if(event.clickCount==2){[self.window zoom:nil];}else{[self.window performWindowDragWithEvent:event];}
}
@end
@interface TalaDashboard : NSObject <WKScriptMessageHandler, WKUIDelegate, WKNavigationDelegate, NSWindowDelegate>
@property(strong) NSWindow *window;
@property(strong) TalaWebView *web;
@property(strong) NSWindow *playerWindow;
@property(strong) AVPlayerView *player;
@property(copy) NSString *playingPath;
@property BOOL quitting;
@property(strong) NSStatusItem *statusItem;
@end
static TalaDashboard *host;
static void request(NSString *action,id data) {
 NSData *json=[NSJSONSerialization dataWithJSONObject:@{@"id":@(-1),@"action":action,@"data":data?:NSNull.null} options:0 error:nil];
 NSString *text=[[NSString alloc]initWithData:json encoding:NSUTF8StringEncoding];goDashboardMessage((char *)text.UTF8String);
}
static NSArray *filePaths(NSPasteboard *pasteboard) {
 NSArray *urls=[pasteboard readObjectsForClasses:@[NSURL.class] options:@{NSPasteboardURLReadingFileURLsOnlyKey:@YES}];
 NSMutableArray *paths=[NSMutableArray array];
 for(NSURL *url in urls){NSNumber *regular=nil;[url getResourceValue:&regular forKey:NSURLIsRegularFileKey error:nil];if(regular.boolValue)[paths addObject:url.path];}
 return paths;
}
static void diagnostic(NSString *message){NSData *data=[NSJSONSerialization dataWithJSONObject:@{@"id":@(-1),@"action":@"diagnostic",@"data":message} options:0 error:nil];NSString *text=[[NSString alloc]initWithData:data encoding:NSUTF8StringEncoding];goDashboardMessage((char *)text.UTF8String);}
static void sendEvent(NSString *type,id data) {
 NSData *json=[NSJSONSerialization dataWithJSONObject:@{@"type":type,@"data":data?:[NSNull null]} options:NSJSONWritingFragmentsAllowed error:nil];
 NSString *s=[[NSString alloc]initWithData:json encoding:NSUTF8StringEncoding];
 [host.web evaluateJavaScript:[NSString stringWithFormat:@"window.receive && window.receive(%@)",s] completionHandler:nil];
}
@implementation TalaWebView
- (NSDragOperation)draggingEntered:(id<NSDraggingInfo>)sender {return filePaths(sender.draggingPasteboard).count?NSDragOperationCopy:NSDragOperationNone;}
- (NSDragOperation)draggingUpdated:(id<NSDraggingInfo>)sender {return [self draggingEntered:sender];}
- (BOOL)prepareForDragOperation:(id<NSDraggingInfo>)sender {return filePaths(sender.draggingPasteboard).count>0;}
- (BOOL)performDragOperation:(id<NSDraggingInfo>)sender {
 NSArray *paths=filePaths(sender.draggingPasteboard);
 if(paths.count){sendEvent(@"files",paths);return YES;}return NO;
}
@end
@implementation TalaDashboard
- (void)userContentController:(WKUserContentController *)controller didReceiveScriptMessage:(WKScriptMessage *)message {
 if(!message.frameInfo.isMainFrame)return;
 if(![message.body isKindOfClass:[NSDictionary class]])return;
 if([message.body[@"action"] isEqual:@"droppedFiles"]){
  NSArray *paths=filePaths([NSPasteboard pasteboardWithName:NSPasteboardNameDrag]);
  if(paths.count){sendEvent(@"files",paths);}else{sendEvent(@"error",@"Drop audio or video files from Finder. Folders and promised downloads are not supported.");}
  sendEvent(@"reply",@{@"id":message.body[@"id"]?:@(-1),@"result":NSNull.null});
  return;
 }
 if([message.body[@"action"] isEqual:@"preference"]){NSDictionary *data=message.body[@"data"];if([@[@"displayMode",@"fontSize",@"selectedSession",@"historySort",@"historyGroup",@"colorScheme",@"appearance"] containsObject:data[@"key"]]&&[data[@"value"] isKindOfClass:NSString.class]){[NSUserDefaults.standardUserDefaults setObject:data[@"value"] forKey:[@"Tala." stringByAppendingString:data[@"key"]]];}return;}
 NSData *data=[NSJSONSerialization dataWithJSONObject:message.body options:0 error:nil];
 if(data.length>4*1024*1024)return;
 NSString *s=[[NSString alloc]initWithData:data encoding:NSUTF8StringEncoding];goDashboardMessage((char*)s.UTF8String);
}
- (void)webView:(WKWebView *)web decidePolicyForNavigationAction:(WKNavigationAction *)action decisionHandler:(void (^)(WKNavigationActionPolicy))handler {
 NSString *scheme=action.request.URL.scheme;
 handler(([scheme isEqual:@"about"]||!scheme)?WKNavigationActionPolicyAllow:WKNavigationActionPolicyCancel);
}
- (void)webView:(WKWebView *)web runJavaScriptConfirmPanelWithMessage:(NSString *)message initiatedByFrame:(WKFrameInfo *)frame completionHandler:(void (^)(BOOL))completion {
 NSAlert *alert=[NSAlert new];alert.messageText=@"Tala";alert.informativeText=message;[alert addButtonWithTitle:@"Confirm"];[alert addButtonWithTitle:@"Cancel"];
 [alert beginSheetModalForWindow:self.window completionHandler:^(NSModalResponse result){completion(result==NSAlertFirstButtonReturn);}];
}
- (void)quitTala:(id)sender {if(self.quitting)return;self.quitting=YES;goDashboardMessage("{\"id\":-1,\"action\":\"quit\"}");}
- (void)showDashboard:(id)sender {dashboardShow();}
- (void)startRecording:(id)sender {request(@"start",nil);}
- (void)stopRecording:(id)sender {request(@"stop",nil);}
- (void)setSource:(NSMenuItem *)sender {request(@"mode",@(sender.tag));}
- (void)openStorage:(id)sender {request(@"reveal",@"");}
- (BOOL)windowShouldClose:(NSWindow *)sender {[sender orderOut:nil];return NO;}
- (void)webView:(WKWebView *)web didFailProvisionalNavigation:(WKNavigation *)nav withError:(NSError *)error {diagnostic(error.localizedDescription);}
- (void)webView:(WKWebView *)web didFailNavigation:(WKNavigation *)nav withError:(NSError *)error {diagnostic(error.localizedDescription);}
- (void)webView:(WKWebView *)web didFinishNavigation:(WKNavigation *)navigation {diagnostic(@"Dashboard loaded");goDashboardMessage("{\"id\":0,\"action\":\"snapshot\"}");}
- (void)webViewWebContentProcessDidTerminate:(WKWebView *)web {diagnostic(@"Web content process terminated");[web reload];}
@end
@interface TalaApplicationDelegate : NSObject <NSApplicationDelegate>
@end
@implementation TalaApplicationDelegate
- (void)applicationDidFinishLaunching:(NSNotification *)notification {goDashboardReady();}
- (BOOL)applicationShouldHandleReopen:(NSApplication *)app hasVisibleWindows:(BOOL)visible {dashboardShow();return YES;}
- (NSApplicationTerminateReply)applicationShouldTerminate:(NSApplication *)app {if(!host)return NSTerminateNow;[host quitTala:nil];return NSTerminateCancel;}
@end
void dashboardRun(void) {
 @autoreleasepool {
  [NSApplication sharedApplication];[NSApp setActivationPolicy:NSApplicationActivationPolicyRegular];
  TalaApplicationDelegate *delegate=[TalaApplicationDelegate new];NSApp.delegate=delegate;[NSApp run];
 }
}
void dashboardStop(void) {dispatch_async(dispatch_get_main_queue(),^{[NSApp stop:nil];[NSApp postEvent:[NSEvent otherEventWithType:NSEventTypeApplicationDefined location:NSZeroPoint modifierFlags:0 timestamp:0 windowNumber:0 context:nil subtype:0 data1:0 data2:0] atStart:YES];});}
void dashboardStartupError(const char *message) {NSString *text=[NSString stringWithUTF8String:message];dispatch_async(dispatch_get_main_queue(),^{NSAlert *alert=[NSAlert new];alert.messageText=@"Cannot open transcript storage";alert.informativeText=[text stringByAppendingString:@"\n\nCheck folder access in System Settings → Privacy & Security, or choose another transcript folder with --output."];[alert runModal];dashboardStop();});}
void dashboardMenuBar(int enabled,int active,int mode) {dispatch_async(dispatch_get_main_queue(),^{
 if(!host)return;
 if(!enabled){if(host.statusItem){[NSStatusBar.systemStatusBar removeStatusItem:host.statusItem];host.statusItem=nil;}return;}
 if(!host.statusItem){host.statusItem=[NSStatusBar.systemStatusBar statusItemWithLength:NSVariableStatusItemLength];}
 host.statusItem.button.image=[NSImage imageWithSystemSymbolName:@"waveform" accessibilityDescription:@"Tala"];
 host.statusItem.button.contentTintColor=active?NSColor.systemRedColor:nil;
 host.statusItem.button.toolTip=active?@"Tala — recording":@"Tala";
 NSMenu *menu=[NSMenu new];menu.autoenablesItems=NO;
 NSMenuItem *(^item)(NSString *,SEL)=^NSMenuItem *(NSString *title,SEL action){NSMenuItem *entry=[menu addItemWithTitle:title action:action keyEquivalent:@""];entry.target=host;return entry;};
 item(@"Open Tala",@selector(showDashboard:));[menu addItem:NSMenuItem.separatorItem];
 item(@"Start Recording",@selector(startRecording:)).enabled=!active;
 item(@"Stop Recording",@selector(stopRecording:)).enabled=active;
 NSArray *sources=@[@"Microphone + System Audio",@"Microphone",@"System Audio"];
 for(NSInteger i=0;i<sources.count;i++){NSMenuItem *entry=item(sources[i],@selector(setSource:));entry.tag=i;entry.state=mode==i?NSControlStateValueOn:NSControlStateValueOff;entry.enabled=!active;}
 [menu addItem:NSMenuItem.separatorItem];item(@"Open Transcript Folder",@selector(openStorage:));
 [menu addItem:NSMenuItem.separatorItem];item(@"Quit Tala",@selector(quitTala:));host.statusItem.menu=menu;
 });}
static NSString *activationName(void) {
 NSString *identifier=NSBundle.mainBundle.bundleIdentifier ?: @"io.github.ajtn05.tala.next";
 return [identifier stringByAppendingString:@".show"];
}
void dashboardActivate(void){[[NSDistributedNotificationCenter defaultCenter] postNotificationName:activationName() object:nil userInfo:nil deliverImmediately:YES];}
void dashboardInit(const char *html) {
 NSString *page=[NSString stringWithUTF8String:html];
 dispatch_async(dispatch_get_main_queue(),^{
 if(host)return;host=[TalaDashboard new];
 [NSApp setActivationPolicy:NSApplicationActivationPolicyRegular];
 [[NSDistributedNotificationCenter defaultCenter] addObserverForName:activationName() object:nil queue:[NSOperationQueue mainQueue] usingBlock:^(NSNotification *n){dashboardShow();}];
 WKWebViewConfiguration *configuration=[WKWebViewConfiguration new];configuration.websiteDataStore=[WKWebsiteDataStore nonPersistentDataStore];
 [configuration.userContentController addScriptMessageHandler:host name:@"tala"];
 NSMutableDictionary *preferences=[NSMutableDictionary dictionary];for(NSString *key in @[@"displayMode",@"fontSize",@"selectedSession",@"historySort",@"historyGroup",@"colorScheme",@"appearance"]){NSString *value=[NSUserDefaults.standardUserDefaults stringForKey:[@"Tala." stringByAppendingString:key]];if(value)preferences[key]=value;}
 NSData *prefs=[NSJSONSerialization dataWithJSONObject:preferences options:0 error:nil];NSString *prefsJSON=[[NSString alloc]initWithData:prefs encoding:NSUTF8StringEncoding];
 [configuration.userContentController addUserScript:[[WKUserScript alloc]initWithSource:[NSString stringWithFormat:@"window.talaPreferences=%@;",prefsJSON] injectionTime:WKUserScriptInjectionTimeAtDocumentStart forMainFrameOnly:YES]];
 host.web=[[TalaWebView alloc]initWithFrame:NSMakeRect(0,0,1100,760) configuration:configuration];host.web.UIDelegate=host;host.web.navigationDelegate=host;
 [host.web registerForDraggedTypes:@[NSPasteboardTypeFileURL]];
 host.window=[[NSWindow alloc]initWithContentRect:NSMakeRect(0,0,1100,760) styleMask:NSWindowStyleMaskTitled|NSWindowStyleMaskClosable|NSWindowStyleMaskMiniaturizable|NSWindowStyleMaskResizable|NSWindowStyleMaskFullSizeContentView backing:NSBackingStoreBuffered defer:NO];
 host.window.title=@"Tala";host.window.minSize=NSMakeSize(680,480);host.window.delegate=host;host.window.releasedWhenClosed=NO;
 host.window.titlebarAppearsTransparent=YES;host.window.titleVisibility=NSWindowTitleHidden;host.window.toolbarStyle=NSWindowToolbarStyleUnifiedCompact;
 [host.window center];
 [host.window setFrameAutosaveName:@"TalaDashboard"];
 diagnostic([NSString stringWithFormat:@"Loading %lu bytes of bundled UI",(unsigned long)page.length]);host.window.contentView=host.web;[host.web loadHTMLString:page baseURL:nil];
 CGFloat dragY=host.web.isFlipped?0:host.web.bounds.size.height-40;
 TalaDragView *drag=[[TalaDragView alloc]initWithFrame:NSMakeRect(0,dragY,host.web.bounds.size.width-320,40)];
 drag.autoresizingMask=NSViewWidthSizable|(host.web.isFlipped?NSViewMaxYMargin:NSViewMinYMargin);[host.web addSubview:drag];
 // Supply standard responder-chain editing shortcuts.
 NSMenu *main=[NSMenu new];NSApp.mainMenu=main;
 NSMenuItem *applicationMenu=[NSMenuItem new];applicationMenu.submenu=[[NSMenu alloc]initWithTitle:@"Tala"];
 [applicationMenu.submenu addItemWithTitle:@"Hide Tala" action:@selector(hide:) keyEquivalent:@"h"];
 NSMenuItem *hideOthers=[applicationMenu.submenu addItemWithTitle:@"Hide Others" action:@selector(hideOtherApplications:) keyEquivalent:@"h"];hideOthers.keyEquivalentModifierMask=NSEventModifierFlagCommand|NSEventModifierFlagOption;
 [applicationMenu.submenu addItemWithTitle:@"Show All" action:@selector(unhideAllApplications:) keyEquivalent:@""];
 [applicationMenu.submenu addItem:[NSMenuItem separatorItem]];
 NSMenuItem *quit=[applicationMenu.submenu addItemWithTitle:@"Quit Tala" action:@selector(quitTala:) keyEquivalent:@"q"];quit.target=host;[main addItem:applicationMenu];
 NSMenuItem *edit=[NSMenuItem new];edit.title=@"Edit";edit.submenu=[[NSMenu alloc]initWithTitle:@"Edit"];
 NSArray *titles=@[@"Undo",@"Redo",@"Cut",@"Copy",@"Paste",@"Select All"];
 NSArray *actions=@[@"undo:",@"redo:",@"cut:",@"copy:",@"paste:",@"selectAll:"];NSArray *keys=@[@"z",@"Z",@"x",@"c",@"v",@"a"];
 for(NSUInteger i=0;i<titles.count;i++){[edit.submenu addItemWithTitle:titles[i] action:NSSelectorFromString(actions[i]) keyEquivalent:keys[i]];}[main addItem:edit];
 NSMenuItem *windowMenu=[NSMenuItem new];windowMenu.title=@"Window";windowMenu.submenu=[[NSMenu alloc]initWithTitle:@"Window"];
 [windowMenu.submenu addItemWithTitle:@"Close" action:@selector(performClose:) keyEquivalent:@"w"];
 [windowMenu.submenu addItemWithTitle:@"Minimize" action:@selector(performMiniaturize:) keyEquivalent:@"m"];
 [windowMenu.submenu addItemWithTitle:@"Zoom" action:@selector(performZoom:) keyEquivalent:@""];[main addItem:windowMenu];NSApp.windowsMenu=windowMenu.submenu;
 [host.window makeKeyAndOrderFront:nil];[NSApp activateIgnoringOtherApps:YES];
 });
}
void dashboardShow(void){dispatch_async(dispatch_get_main_queue(),^{if(host.window.miniaturized)[host.window deminiaturize:nil];[host.window makeKeyAndOrderFront:nil];[NSApp activateIgnoringOtherApps:YES];});}
void dashboardQuit(void){dispatch_async(dispatch_get_main_queue(),^{[host quitTala:nil];});}
void dashboardSend(const char *json){NSString *s=[NSString stringWithUTF8String:json];dispatch_semaphore_t ack=dispatch_semaphore_create(0);dispatch_async(dispatch_get_main_queue(),^{if(host.web){[host.web evaluateJavaScript:[NSString stringWithFormat:@"window.receive && window.receive(%@)",s] completionHandler:^(id value,NSError *error){dispatch_semaphore_signal(ack);}];}else{dispatch_semaphore_signal(ack);}});dispatch_semaphore_wait(ack,dispatch_time(DISPATCH_TIME_NOW,NSEC_PER_SEC));}
void dashboardPickFiles(int mode){dispatch_async(dispatch_get_main_queue(),^{
 NSOpenPanel *panel=[NSOpenPanel openPanel];panel.canChooseDirectories=NO;panel.allowsMultipleSelection=mode==0;
 panel.message=mode==1?@"Choose a verified Whisper Large v3 Turbo GGML model":mode==2?@"Choose a Netscape cookies.txt export from your signed-in browser":@"Choose audio or video files";
 [panel beginSheetModalForWindow:host.window completionHandler:^(NSModalResponse result){if(result==NSModalResponseOK){
  NSMutableArray *paths=[NSMutableArray array];for(NSURL *url in panel.URLs){[paths addObject:url.path];}
  sendEvent(mode==1?@"modelFile":mode==2?@"cookiesFile":@"files",paths);
 }}];
});}
static void copyText(const char *text,NSString *message){NSString *s=[NSString stringWithUTF8String:text];dispatch_async(dispatch_get_main_queue(),^{[NSPasteboard.generalPasteboard clearContents];[NSPasteboard.generalPasteboard setString:s forType:NSPasteboardTypeString];sendEvent(@"toast",message);});}
void dashboardCopy(const char *text){copyText(text,@"Transcript copied");}
void dashboardCopyURL(const char *text){copyText(text,@"Sign-in URL copied");}
void dashboardSave(const char *name,const char *text){NSString *n=[NSString stringWithUTF8String:name],*s=[NSString stringWithUTF8String:text];dispatch_async(dispatch_get_main_queue(),^{NSSavePanel *panel=[NSSavePanel savePanel];panel.nameFieldStringValue=n;[panel beginSheetModalForWindow:host.window completionHandler:^(NSModalResponse result){if(result==NSModalResponseOK){NSError *error=nil;[s writeToURL:panel.URL atomically:YES encoding:NSUTF8StringEncoding error:&error];sendEvent(error?@"error":@"toast",error?error.localizedDescription:@"Transcript exported");}}];});}
void dashboardTop(int enabled){dispatch_async(dispatch_get_main_queue(),^{host.window.level=enabled?NSFloatingWindowLevel:NSNormalWindowLevel;});}
void dashboardPlay(const char *path,double seconds){NSString *p=[NSString stringWithUTF8String:path];dispatch_async(dispatch_get_main_queue(),^{if(!host.playerWindow){host.playerWindow=[[NSWindow alloc]initWithContentRect:NSMakeRect(0,0,640,360) styleMask:NSWindowStyleMaskTitled|NSWindowStyleMaskClosable|NSWindowStyleMaskResizable backing:NSBackingStoreBuffered defer:NO];host.playerWindow.title=@"Review media";host.playerWindow.releasedWhenClosed=NO;host.player=[[AVPlayerView alloc]initWithFrame:NSMakeRect(0,0,640,360)];host.player.controlsStyle=AVPlayerViewControlsStyleFloating;host.playerWindow.contentView=host.player;[host.playerWindow center];}
 if(![host.playingPath isEqual:p]){[host.player.player pause];host.player.player=[AVPlayer playerWithURL:[NSURL fileURLWithPath:p]];host.playingPath=p;}
 [host.player.player seekToTime:CMTimeMakeWithSeconds(seconds,600) toleranceBefore:kCMTimeZero toleranceAfter:kCMTimeZero];[host.playerWindow makeKeyAndOrderFront:nil];
 });}
