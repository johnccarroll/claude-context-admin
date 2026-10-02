// Claude Context Admin for macOS: a native window around the cca engine.
//
// The app starts the bundled `cca` (Contents/Resources/cca) on a free local port and shows its UI
// in a WKWebView. Closing the window keeps it running in the background, with a menu bar icon, so
// the engine keeps recording Claude's memory edits; Quit stops both. For development, set CCA_URL
// to a running server (scripts/dev.sh) and the app shows that instead of starting its own.
import Cocoa
import ServiceManagement
import WebKit

@main
final class App: NSObject, NSApplicationDelegate, NSWindowDelegate, WKNavigationDelegate, WKUIDelegate,
  WKScriptMessageHandler, NSMenuDelegate
{
  static func main() {
    let app = NSApplication.shared
    let delegate = App()
    app.delegate = delegate
    app.run()
  }

  private var window: NSWindow!
  private var web: WKWebView!
  private var status: NSStatusItem!
  private var engine: Process?
  private var origin: (host: String, port: Int)?  // the engine's address: the only page this window loads
  private var quitting = false
  private var lastMouseDown: NSEvent?
  private let loginItem = NSMenuItem(title: "Open at Login", action: #selector(toggleLogin), keyEquivalent: "")

  // MARK: launch and quit

  func applicationDidFinishLaunching(_ note: Notification) {
    buildMainMenu()
    buildWindow()
    buildStatusItem()
    // A drag in the page's top bars moves the window (see the injected script below).
    NSEvent.addLocalMonitorForEvents(matching: .leftMouseDown) { [weak self] e in
      self?.lastMouseDown = e
      return e
    }
    if let dev = ProcessInfo.processInfo.environment["CCA_URL"], let url = URL(string: dev) {
      load(url)
    } else {
      startEngine()
    }
    showWindow()
  }

  func applicationShouldTerminateAfterLastWindowClosed(_ app: NSApplication) -> Bool { false }

  func applicationShouldHandleReopen(_ app: NSApplication, hasVisibleWindows: Bool) -> Bool {
    showWindow()
    return true
  }

  func applicationWillTerminate(_ note: Notification) {
    quitting = true
    if let e = engine, e.isRunning {
      e.terminate()  // SIGTERM: cca shuts down its server cleanly
      e.waitUntilExit()
    }
  }

  // MARK: the cca engine

  private func load(_ url: URL) {
    origin = (url.host ?? "", url.port ?? 80)
    web.load(URLRequest(url: url))
  }

  private func startEngine() {
    guard let cca = Bundle.main.url(forResource: "cca", withExtension: nil) else {
      showProblem("This copy of the app is missing its engine (Contents/Resources/cca). Reinstall it.")
      return
    }
    // Finding `claude` can mean asking a login shell, which may be slow: never on the main thread.
    DispatchQueue.global(qos: .userInitiated).async {
      let path = Self.enginePath()
      DispatchQueue.main.async { self.launch(cca, path: path) }
    }
  }

  private func launch(_ cca: URL, path: String) {
    let p = Process()
    p.executableURL = cca
    // A fixed port keeps the page's origin, and so its saved theme and accent, the same across
    // launches; cca takes a free one if something else holds it.
    p.arguments = ["--no-open", "--port", "4318"]
    var env = ProcessInfo.processInfo.environment
    env["PATH"] = path  // Finder starts apps with a bare PATH; cca needs `claude`
    p.environment = env
    let out = Pipe()
    p.standardOutput = out
    p.standardError = logFile()
    out.fileHandleForReading.readabilityHandler = { [weak self] h in
      let text = String(decoding: h.availableData, as: UTF8.self)
      guard let r = text.range(of: #"http://127\.0\.0\.1:[0-9]+/\?t=[0-9a-f]+"#, options: .regularExpression),
        let url = URL(string: String(text[r]))
      else { return }
      h.readabilityHandler = { _ = $0.availableData }  // keep draining so cca never blocks on a full pipe
      DispatchQueue.main.async { self?.load(url) }
    }
    p.terminationHandler = { [weak self] proc in
      DispatchQueue.main.async {
        guard let self, !self.quitting else { return }
        self.origin = nil // nothing may load in the window now: another program could take the port
        self.showProblem("The engine stopped (exit \(proc.terminationStatus)). Details are in ~/Library/Logs/Claude Context Admin.log. Quit and reopen the app.")
      }
    }
    do {
      try p.run()
      engine = p
    } catch {
      showProblem("Couldn't start the engine: \(error.localizedDescription)")
    }
  }

  /// A PATH where cca finds the `claude` CLI however the app was opened. The usual install places
  /// come first; only when `claude` isn't in any of them is a login shell asked, non-interactively
  /// (no .zshrc), with a hard 3-second limit that doesn't wait on anything it left running.
  private static func enginePath() -> String {
    let home = NSHomeDirectory()
    let fallback = [home + "/.local/bin", "/opt/homebrew/bin", "/usr/local/bin", home + "/.claude/local", home + "/.npm-global/bin",
      home + "/.bun/bin", "/usr/bin", "/bin", "/usr/sbin", "/sbin"]
    if fallback.contains(where: { FileManager.default.isExecutableFile(atPath: $0 + "/claude") }) {
      return fallback.joined(separator: ":")
    }
    let p = Process()
    p.executableURL = URL(fileURLWithPath: ProcessInfo.processInfo.environment["SHELL"] ?? "/bin/zsh")
    p.arguments = ["-l", "-c", "/usr/bin/printenv PATH"]  // printenv: colon-separated in every shell, fish too
    let out = Pipe()
    p.standardOutput = out
    p.standardError = FileHandle.nullDevice
    p.standardInput = FileHandle.nullDevice
    let lock = NSLock()
    var data = Data()
    out.fileHandleForReading.readabilityHandler = { h in
      let d = h.availableData
      lock.lock(); data.append(d); lock.unlock()
    }
    guard (try? p.run()) != nil else { return fallback.joined(separator: ":") }
    let deadline = Date().addingTimeInterval(3)
    while p.isRunning && Date() < deadline { usleep(20_000) }
    if p.isRunning { p.terminate() }
    usleep(50_000)
    out.fileHandleForReading.readabilityHandler = nil
    lock.lock(); let path = String(decoding: data, as: UTF8.self); lock.unlock()
    var parts = path.trimmingCharacters(in: .whitespacesAndNewlines).split(separator: ":").map(String.init).filter { $0.hasPrefix("/") }
    for f in fallback where !parts.contains(f) { parts.append(f) }
    return parts.joined(separator: ":")
  }

  private func logFile() -> FileHandle {
    let url = FileManager.default.homeDirectoryForCurrentUser.appendingPathComponent("Library/Logs/Claude Context Admin.log")
    FileManager.default.createFile(atPath: url.path, contents: nil)
    return (try? FileHandle(forWritingTo: url)) ?? FileHandle.nullDevice
  }

  private func showProblem(_ message: String) {
    let esc = message.replacingOccurrences(of: "&", with: "&amp;").replacingOccurrences(of: "<", with: "&lt;")
    web.loadHTMLString(
      "<body style=\"font:15px -apple-system;background:#0b0b0d;color:#ddd;display:grid;place-items:center;height:90vh;padding:24px\"><p style=\"max-width:520px\">\(esc)</p></body>",
      baseURL: nil)
  }

  // MARK: window

  private func buildWindow() {
    window = NSWindow(
      contentRect: NSRect(x: 0, y: 0, width: 1280, height: 820),
      styleMask: [.titled, .closable, .miniaturizable, .resizable, .fullSizeContentView],
      backing: .buffered, defer: false)
    window.title = "Claude Context Admin"
    window.titleVisibility = .hidden
    window.titlebarAppearsTransparent = true
    let bar = NSToolbar(identifier: "main")  // an empty unified toolbar gives the taller title bar
    bar.showsBaselineSeparator = false
    window.toolbar = bar
    window.toolbarStyle = .unified
    window.minSize = NSSize(width: 820, height: 520)
    window.backgroundColor = NSColor(srgbRed: 0.035, green: 0.035, blue: 0.043, alpha: 1)
    window.isReleasedWhenClosed = false
    window.delegate = self
    if !window.setFrameUsingName("Main") { window.center() }
    window.setFrameAutosaveName("Main")

    let config = WKWebViewConfiguration()
    let script = """
      document.documentElement.classList.add('app-shell');
      const free = (t) => !t.closest('button,input,select,textarea,a,label,[contenteditable],.chip,.prow,.search,.menu,.settings');
      const bar = (t) => t.closest('.brand,.top,.side .foot');
      addEventListener('mousedown', (e) => { if (e.button === 0 && bar(e.target) && free(e.target)) webkit.messageHandlers.cca.postMessage('drag'); });
      addEventListener('dblclick', (e) => { if (bar(e.target) && free(e.target)) webkit.messageHandlers.cca.postMessage('zoom'); });
      """
    config.userContentController.addUserScript(WKUserScript(source: script, injectionTime: .atDocumentStart, forMainFrameOnly: true))
    config.userContentController.add(self, name: "cca")
    web = WKWebView(frame: .zero, configuration: config)
    web.navigationDelegate = self
    web.allowsBackForwardNavigationGestures = true // two-finger swipe goes back and forward between pages
    web.uiDelegate = self
    web.setValue(false, forKey: "drawsBackground")  // no white flash before the page paints
    // Web Inspector only when asked for: defaults write dev.johncarroll.claude-context-admin inspectable -bool YES
    if #available(macOS 13.3, *) { web.isInspectable = UserDefaults.standard.bool(forKey: "inspectable") }
    window.contentView = web
  }

  @objc private func showWindow() {
    NSApp.setActivationPolicy(.regular)
    window.makeKeyAndOrderFront(nil)
    NSApp.activate(ignoringOtherApps: true)
  }

  /// Closing hides the window; the app stays in the menu bar and the engine keeps watching.
  func windowShouldClose(_ sender: NSWindow) -> Bool {
    window.orderOut(nil)
    NSApp.setActivationPolicy(.accessory)  // no Dock icon while it's only in the menu bar
    return false
  }

  func userContentController(_ c: WKUserContentController, didReceive m: WKScriptMessage) {
    switch m.body as? String {
    case "drag": if let e = lastMouseDown { window.performDrag(with: e) }
    case "zoom": window.zoom(nil)
    default: break
    }
  }

  // Only the engine's own address loads inside the window. A clicked web link opens in the browser;
  // anything else (file:, smb:, other apps' URL schemes, redirects, scripted navigation) is dropped.
  func webView(_ w: WKWebView, decidePolicyFor a: WKNavigationAction, decisionHandler: @escaping (WKNavigationActionPolicy) -> Void) {
    guard let url = a.request.url else { return decisionHandler(.cancel) }
    if url.scheme == "about" || (url.scheme == "http" && origin.map { $0.host == url.host && $0.port == (url.port ?? 80) } == true) {
      return decisionHandler(.allow)
    }
    if a.navigationType == .linkActivated { openOutside(url) }
    decisionHandler(.cancel)
  }

  func webView(_ w: WKWebView, createWebViewWith c: WKWebViewConfiguration, for a: WKNavigationAction, windowFeatures f: WKWindowFeatures) -> WKWebView? {
    if let url = a.request.url, a.navigationType == .linkActivated { openOutside(url) }
    return nil
  }

  private func openOutside(_ url: URL) {
    if url.scheme == "https" || url.scheme == "http" { NSWorkspace.shared.open(url) }
  }

  /// The pages the menus open, by title and URL hash.
  private let pages = [("Memories", "memories"), ("Review", "review"), ("Map", "map"),
                       ("Activity", "activity"), ("What Loads Here", "what")]

  /// Opens the window on the page in the menu item's representedObject.
  @objc private func go(_ sender: NSMenuItem) {
    guard let page = sender.representedObject as? String else { return }
    showWindow()
    web.evaluateJavaScript("location.hash = '#\(page)'") // the hash change itself fires popstate
  }

  private func page(_ title: String, _ page: String, _ key: String = "") -> NSMenuItem {
    let i = item(title, #selector(go(_:)), key)
    i.representedObject = page
    return i
  }

  @objc private func reload() { web.reload() }
  @objc private func back() { web.goBack() }
  @objc private func forward() { web.goForward() }

  // MARK: menu bar icon

  private func buildStatusItem() {
    status = NSStatusBar.system.statusItem(withLength: NSStatusItem.squareLength)
    let image = NSImage(systemSymbolName: "brain", accessibilityDescription: "Claude Context Admin")
    image?.isTemplate = true
    status.button?.image = image
    let m = NSMenu()
    m.delegate = self
    m.addItem(item("Open Claude Context Admin", #selector(showWindow), ""))
    m.addItem(.separator())
    pages.forEach { m.addItem(page($0.0, $0.1)) }
    m.addItem(.separator())
    loginItem.target = self
    m.addItem(loginItem)
    m.addItem(item("About Claude Context Admin", #selector(about), ""))
    m.addItem(.separator())
    m.addItem(NSMenuItem(title: "Quit Claude Context Admin", action: #selector(NSApplication.terminate(_:)), keyEquivalent: "q"))
    status.menu = m
  }

  func menuWillOpen(_ menu: NSMenu) {
    loginItem.state = SMAppService.mainApp.status == .enabled ? .on : .off
  }

  /// Opt-in: registers the app itself as a login item (System Settings → General → Login Items).
  @objc private func toggleLogin() {
    do {
      if SMAppService.mainApp.status == .enabled {
        try SMAppService.mainApp.unregister()
      } else {
        try SMAppService.mainApp.register()
      }
    } catch {
      let a = NSAlert()
      a.messageText = "Couldn't change Open at Login"
      a.informativeText = error.localizedDescription
      a.runModal()
    }
  }

  @objc private func about() {
    showWindow()
    NSApp.orderFrontStandardAboutPanel(nil)
  }

  /// A menu item. The app's own actions target it; standard ones (Copy, Hide, Minimize…) go
  /// up the responder chain.
  private func item(_ title: String, _ action: Selector, _ key: String = "", _ mods: NSEvent.ModifierFlags = .command) -> NSMenuItem {
    let i = NSMenuItem(title: title, action: action, keyEquivalent: key)
    i.keyEquivalentModifierMask = mods
    i.target = responds(to: action) ? self : nil
    return i
  }

  // MARK: main menu

  private func buildMainMenu() {
    let main = NSMenu()
    @discardableResult func sub(_ title: String, _ items: [NSMenuItem]) -> NSMenu {
      let top = NSMenuItem(title: title, action: nil, keyEquivalent: "")
      let m = NSMenu(title: title)
      items.forEach(m.addItem)
      top.submenu = m
      main.addItem(top)
      return m
    }
    sub("Claude Context Admin", [
      item("About Claude Context Admin", #selector(about), ""),
      .separator(),
      item("Hide Claude Context Admin", #selector(NSApplication.hide(_:)), "h"),
      item("Hide Others", #selector(NSApplication.hideOtherApplications(_:)), "h", [.command, .option]),
      item("Show All", #selector(NSApplication.unhideAllApplications(_:)), ""),
      .separator(),
      item("Quit Claude Context Admin", #selector(NSApplication.terminate(_:)), "q"),
    ])
    sub("Edit", [  // text fields in the page need these for ⌘C, ⌘V, ⌘Z
      item("Undo", Selector(("undo:")), "z"),
      item("Redo", Selector(("redo:")), "z", [.command, .shift]),
      .separator(),
      item("Cut", #selector(NSText.cut(_:)), "x"),
      item("Copy", #selector(NSText.copy(_:)), "c"),
      item("Paste", #selector(NSText.paste(_:)), "v"),
      item("Select All", #selector(NSText.selectAll(_:)), "a"),
    ])
    sub("View", [item("Reload", #selector(reload), "r")])
    sub("Go", [item("Back", #selector(back), "["), item("Forward", #selector(forward), "]"), .separator()]
      + pages.enumerated().map { page($1.0, $1.1, "\($0 + 1)") })
    NSApp.windowsMenu = sub("Window", [
      item("Minimize", #selector(NSWindow.performMiniaturize(_:)), "m"),
      item("Zoom", #selector(NSWindow.performZoom(_:))),
      item("Close", #selector(NSWindow.performClose(_:)), "w"),
      .separator(),
      item("Bring All to Front", #selector(NSApplication.arrangeInFront(_:))),
    ])
    NSApp.mainMenu = main
  }
}
