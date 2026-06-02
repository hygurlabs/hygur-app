import SwiftUI
import WebKit
import AppKit
import AVFoundation
import Speech
import EventKit
import UserNotifications

/// The WKWebView shell. The entire Hygur interface is now the React/TS web app
/// served by the sidecar (http://localhost:8420); this native window is a thin
/// container that (1) hosts the web view and (2) exposes a small JS↔Swift bridge
/// (`window.HygurNative`) for the capabilities the web layer can't reach itself:
/// the macOS Calendar (EventKit) and local notifications.
///
/// Native-only surfaces that aren't migrated yet (model settings, connectors)
/// remain reachable through the Settings scene (Cmd+,) and the menu-bar item,
/// both of which live independently of this window's content.
struct WebShellView: View {
    @Environment(\.openWindow) private var openWindow
    @Environment(VoiceService.self) private var voice
    @State private var loaded = false
    @State private var failed = false
    @State private var store = WebViewStore()

    private var rootURL: URL {
        AppPreferences.shared.sidecarURLValue ?? URL(string: "http://localhost:8420")!
    }

    var body: some View {
        ZStack {
            WebContainer(url: rootURL, store: store, voice: voice, loaded: $loaded, failed: $failed)

            if !loaded {
                VStack(spacing: 14) {
                    if failed {
                        Text("Couldn't reach the Hygur runtime.")
                            .foregroundStyle(.secondary)
                        Text("It usually starts within a few seconds.")
                            .font(.callout)
                            .foregroundStyle(.tertiary)
                    } else {
                        ProgressView()
                        Text("Starting Hygur…")
                            .foregroundStyle(.secondary)
                    }
                }
                .frame(maxWidth: .infinity, maxHeight: .infinity)
                .background(Color(nsColor: .windowBackgroundColor))
                .transition(.opacity)
            }
        }
        .animation(.easeOut(duration: 0.2), value: loaded)
        // Native commands → the web app. The menu-bar "Ask Hygur" field and the
        // global hotkey post these; we drive the SPA's hash router so the query
        // lands in the Ask view (deep-link) without needing a fresh page load.
        .onReceive(NotificationCenter.default.publisher(for: .focusChatInput)) { note in
            let prefill = (note.object as? String)?
                .trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
            if prefill.isEmpty { store.navigate(route: "/") } else { store.ask(prefill) }
        }
        .onReceive(NotificationCenter.default.publisher(for: .navigateToSection)) { note in
            store.navigate(section: note.object as? String ?? "chat")
        }
        .onAppear {
            // Let summon / Dock-reopen recreate this window after it's closed.
            WindowAccess.shared.openMainWindow = { openWindow(id: "main") }
        }
        // Push live STT partials into the web composer as the user dictates,
        // and a final value when recording stops. The web side subscribes via
        // window.HygurNative.onDictation.
        .onChange(of: voice.transcript) { _, text in
            store.pushDictation(text: text, isFinal: false)
        }
        .onChange(of: voice.isRecording) { _, recording in
            if !recording {
                store.pushDictation(text: voice.transcript, isFinal: true)
            }
        }
    }
}

// MARK: - Web view store (native → SPA navigation)

/// Holds the live `WKWebView` so SwiftUI-level notification observers can drive
/// the SPA's hash router. Kept separate from the bridge Coordinator so this
/// navigation runs on the main actor with no Sendable/deinit friction.
@MainActor
final class WebViewStore {
    weak var webView: WKWebView?
    private var askCounter = 0

    func navigate(route: String) {
        webView?.evaluateJavaScript("window.location.hash = '#\(route)';", completionHandler: nil)
    }

    func navigate(section: String) {
        let route: String
        switch section {
        case "search": route = "/search"
        case "knowledgeBase": route = "/library"
        case "notes": route = "/notes"
        case "tags": route = "/tags"
        case "calendar": route = "/calendar"
        case "connectors": route = "/connectors"
        case "settings": route = "/settings"
        default: route = "/"
        }
        navigate(route: route)
    }

    /// Deep-links a query into the Ask view. The incrementing nonce makes each
    /// ask a distinct URL so the same text can be re-asked back-to-back.
    func ask(_ query: String) {
        askCounter += 1
        let enc = query.addingPercentEncoding(withAllowedCharacters: .urlQueryAllowed) ?? query
        webView?.evaluateJavaScript(
            "window.location.hash = '#/?q=\(enc)&n=\(askCounter)';",
            completionHandler: nil
        )
    }

    /// Pushes a live speech-to-text transcript into the web composer. The text
    /// is JSON-encoded so quotes/newlines survive the JS bridge.
    func pushDictation(text: String, isFinal: Bool) {
        let literal: String
        if let data = try? JSONEncoder().encode(text), let s = String(data: data, encoding: .utf8) {
            literal = s
        } else {
            literal = "\"\""
        }
        webView?.evaluateJavaScript(
            "window.__hygurDictation && window.__hygurDictation(\(literal), \(isFinal ? "true" : "false"));",
            completionHandler: nil
        )
    }
}

// MARK: - NSViewRepresentable

private struct WebContainer: NSViewRepresentable {
    let url: URL
    let store: WebViewStore
    let voice: VoiceService
    @Binding var loaded: Bool
    @Binding var failed: Bool

    func makeCoordinator() -> Coordinator {
        Coordinator(url: url, voice: voice, loaded: $loaded, failed: $failed)
    }

    func makeNSView(context: Context) -> WKWebView {
        let config = WKWebViewConfiguration()
        let controller = WKUserContentController()
        controller.add(context.coordinator, name: Coordinator.bridgeName)
        controller.addUserScript(
            WKUserScript(
                source: Coordinator.bridgeJS,
                injectionTime: .atDocumentStart,
                forMainFrameOnly: true
            )
        )
        config.userContentController = controller

        let webView = WKWebView(frame: .zero, configuration: config)
        webView.navigationDelegate = context.coordinator
        webView.uiDelegate = context.coordinator // file <input> open panels, JS dialogs
        webView.setValue(false, forKey: "drawsBackground") // let the page paint its own bg
        context.coordinator.webView = webView
        store.webView = webView
        webView.load(URLRequest(url: url))
        return webView
    }

    func updateNSView(_ nsView: WKWebView, context: Context) {}
}

// MARK: - Coordinator (bridge + navigation)

private final class Coordinator: NSObject, WKScriptMessageHandler, WKNavigationDelegate, WKUIDelegate {
    static let bridgeName = "hygur"

    let url: URL
    private let voice: VoiceService
    private let loaded: Binding<Bool>
    private let failed: Binding<Bool>
    weak var webView: WKWebView?
    private var retries = 0

    init(url: URL, voice: VoiceService, loaded: Binding<Bool>, failed: Binding<Bool>) {
        self.url = url
        self.voice = voice
        self.loaded = loaded
        self.failed = failed
        super.init()
    }

    // MARK: Navigation delegate

    func webView(_ webView: WKWebView, didFinish navigation: WKNavigation!) {
        retries = 0
        loaded.wrappedValue = true
        failed.wrappedValue = false
    }

    func webView(_ webView: WKWebView, didFail navigation: WKNavigation!, withError error: Error) {
        scheduleRetry()
    }

    // MARK: UI delegate — file <input> open panel

    /// WKWebView shows NO file picker for `<input type="file">` unless the host
    /// implements this. Without it the composer's 📎 button silently does
    /// nothing. The async, main-actor form matches the audited WKUIDelegate
    /// requirement on current SDKs (the completion-handler form only "nearly
    /// matches" and is never invoked).
    @MainActor
    func webView(_ webView: WKWebView,
                 runOpenPanelWith parameters: WKOpenPanelParameters,
                 initiatedByFrame frame: WKFrameInfo) async -> [URL]? {
        let panel = NSOpenPanel()
        panel.canChooseFiles = true
        panel.canChooseDirectories = parameters.allowsDirectories
        panel.allowsMultipleSelection = parameters.allowsMultipleSelection
        return panel.runModal() == .OK ? panel.urls : nil
    }

    func webView(_ webView: WKWebView, didFailProvisionalNavigation navigation: WKNavigation!, withError error: Error) {
        scheduleRetry()
    }

    /// The sidecar may not have bound its port yet on the first paint. Retry the
    /// load a handful of times before surfacing the error overlay.
    private func scheduleRetry() {
        guard !loaded.wrappedValue else { return }
        if retries >= 20 {
            failed.wrappedValue = true
            return
        }
        retries += 1
        let target = url
        DispatchQueue.main.asyncAfter(deadline: .now() + 1.0) { [weak self] in
            guard let self, !self.loaded.wrappedValue else { return }
            self.webView?.load(URLRequest(url: target))
        }
    }

    // MARK: Bridge

    func userContentController(_ controller: WKUserContentController, didReceive message: WKScriptMessage) {
        guard let body = message.body as? [String: Any],
              let id = (body["id"] as? NSNumber)?.intValue,
              let fn = body["fn"] as? String else { return }
        let args = body["args"] as? [String: Any] ?? [:]
        Task { @MainActor in await self.dispatch(id: id, fn: fn, args: args) }
    }

    @MainActor
    private func dispatch(id: Int, fn: String, args: [String: Any]) async {
        switch fn {
        case "ping":
            resolve(id, ok: true, literal: "{\"ok\":true}")

        case "calendar.authorize":
            let granted = await CalendarService.shared.ensureAuthorized()
            resolve(id, ok: true, literal: granted ? "true" : "false")

        case "calendar.listCalendars":
            let calendars = await CalendarService.shared.calendarSnapshots()
            resolve(id, ok: true, literal: encode(calendars))

        case "calendar.listEvents":
            let hours = (args["hours"] as? NSNumber)?.intValue ?? 168
            do {
                let events = try await CalendarService.shared.eventSnapshots(within: hours, calendarIDs: [])
                resolve(id, ok: true, literal: encode(events))
            } catch {
                resolve(id, ok: false, literal: errorLiteral(error.localizedDescription))
            }

        case "notify":
            let title = args["title"] as? String ?? "Hygur"
            let body = args["body"] as? String ?? ""
            await NotificationsService.shared.postDirect(title: title, body: body)
            resolve(id, ok: true, literal: "{\"ok\":true}")

        case "dictation.start":
            // On-device STT via the existing VoiceService. Partials stream back
            // through window.__hygurDictation (driven from WebShellView).
            if !voice.isOnDeviceAvailable { await voice.prepare() }
            await voice.start()
            resolve(id, ok: true, literal: voice.isOnDeviceAvailable ? "true" : "false")

        case "dictation.stop":
            voice.stop()
            resolve(id, ok: true, literal: encode(voice.transcript))

        case "calendar.setEnabled":
            // Persist the meeting-briefing calendar selection for the native
            // MeetingBriefingScheduler to read.
            let enabled = (args["enabled"] as? Bool) ?? ((args["enabled"] as? NSNumber)?.boolValue ?? false)
            let cals = (args["calendars"] as? [String]) ?? []
            UserDefaults.standard.set(enabled, forKey: "calendar.briefing.enabled")
            UserDefaults.standard.set(cals, forKey: "calendar.briefing.calendars")
            resolve(id, ok: true, literal: "{\"ok\":true}")

        case "prefs.getBool":
            // Read a macOS UserDefaults boolean (notification toggles, etc.) so
            // the WebUI Settings panel can mirror native preferences.
            let key = args["key"] as? String ?? ""
            resolve(id, ok: true, literal: UserDefaults.standard.bool(forKey: key) ? "true" : "false")

        case "prefs.setBool":
            let key = args["key"] as? String ?? ""
            let on = (args["value"] as? Bool) ?? ((args["value"] as? NSNumber)?.boolValue ?? false)
            if !key.isEmpty {
                UserDefaults.standard.set(on, forKey: key)
            }
            // Enabling a notification category triggers the system auth prompt
            // (NotificationsService otherwise requests lazily on first event).
            if on && key.hasPrefix("notify.") {
                await NotificationsService.shared.ensureAuthorization()
            }
            resolve(id, ok: true, literal: "{\"ok\":true}")

        case "perms.status":
            let status = await permissionsStatus()
            resolve(id, ok: true, literal: encode(status))

        case "openExternal":
            // Open a URL in the default browser (Gmail OAuth consent,
            // System Settings deep-links) — WKWebView won't navigate away itself.
            if let s = args["url"] as? String, let url = URL(string: s) {
                NSWorkspace.shared.open(url)
            }
            resolve(id, ok: true, literal: "{\"ok\":true}")

        case "download":
            // Save text content via a native save panel. WKWebView ignores the
            // `download` anchor attribute, so the web layer routes chat exports
            // (Markdown) here.
            let name = args["filename"] as? String ?? "export.txt"
            let content = args["content"] as? String ?? ""
            let saved = await saveTextFile(name: name, content: content)
            resolve(id, ok: true, literal: saved ? "true" : "false")

        case "print":
            // Print the current page via the macOS print panel (PDF export
            // through "Save as PDF"). window.print() is a no-op in WKWebView.
            printWebView()
            resolve(id, ok: true, literal: "{\"ok\":true}")

        default:
            resolve(id, ok: false, literal: errorLiteral("unknown function: \(fn)"))
        }
    }

    /// Snapshot of the macOS permission states the Settings panel surfaces.
    /// Values: "authorized" | "denied" | "notDetermined" | "restricted" |
    /// "writeOnly" | "unknown".
    @MainActor
    private func permissionsStatus() async -> [String: String] {
        let mic: String
        switch AVCaptureDevice.authorizationStatus(for: .audio) {
        case .authorized: mic = "authorized"
        case .denied: mic = "denied"
        case .restricted: mic = "restricted"
        case .notDetermined: mic = "notDetermined"
        @unknown default: mic = "unknown"
        }

        let speech: String
        switch SFSpeechRecognizer.authorizationStatus() {
        case .authorized: speech = "authorized"
        case .denied: speech = "denied"
        case .restricted: speech = "restricted"
        case .notDetermined: speech = "notDetermined"
        @unknown default: speech = "unknown"
        }

        let calendar: String
        switch CalendarService.shared.authorizationStatus {
        case .authorized, .fullAccess: calendar = "authorized"
        case .writeOnly: calendar = "writeOnly"
        case .denied: calendar = "denied"
        case .restricted: calendar = "restricted"
        case .notDetermined: calendar = "notDetermined"
        @unknown default: calendar = "unknown"
        }

        let notifications: String = await withCheckedContinuation { cont in
            UNUserNotificationCenter.current().getNotificationSettings { settings in
                switch settings.authorizationStatus {
                case .authorized, .provisional, .ephemeral: cont.resume(returning: "authorized")
                case .denied: cont.resume(returning: "denied")
                case .notDetermined: cont.resume(returning: "notDetermined")
                @unknown default: cont.resume(returning: "unknown")
                }
            }
        }

        return [
            "microphone": mic,
            "speech": speech,
            "calendar": calendar,
            "notifications": notifications,
        ]
    }

    /// Presents a save panel and writes `content` (UTF-8) to the chosen file.
    /// Returns false if the user cancels or the write fails.
    @MainActor
    private func saveTextFile(name: String, content: String) async -> Bool {
        let panel = NSSavePanel()
        panel.nameFieldStringValue = name
        panel.canCreateDirectories = true
        let response = panel.runModal()
        guard response == .OK, let url = panel.url else { return false }
        do {
            try content.data(using: .utf8)?.write(to: url, options: .atomic)
            return true
        } catch {
            return false
        }
    }

    /// Runs the macOS print panel for the web view (users pick "Save as PDF"
    /// from the panel to export). Falls back to a window-less run if detached.
    @MainActor
    private func printWebView() {
        guard let webView else { return }
        let info = NSPrintInfo.shared
        info.horizontalPagination = .fit
        info.verticalPagination = .automatic
        let op = webView.printOperation(with: info)
        op.showsPrintPanel = true
        op.showsProgressPanel = true
        op.view?.frame = webView.bounds
        if let window = webView.window {
            op.runModal(for: window, delegate: nil, didRun: nil, contextInfo: nil)
        } else {
            op.run()
        }
    }

    /// Resolves the JS-side Promise. The third argument is a raw JS literal —
    /// JSON is a valid JS expression, so encoded values inline directly.
    private func resolve(_ id: Int, ok: Bool, literal: String) {
        let js = "window.__hygurResolve(\(id), \(ok ? "true" : "false"), \(literal));"
        webView?.evaluateJavaScript(js, completionHandler: nil)
    }

    private func encode<T: Encodable>(_ value: T) -> String {
        guard let data = try? JSONEncoder().encode(value),
              let str = String(data: data, encoding: .utf8) else {
            return "null"
        }
        return str
    }

    private func errorLiteral(_ message: String) -> String {
        let dict = ["error": message]
        guard let data = try? JSONSerialization.data(withJSONObject: dict),
              let str = String(data: data, encoding: .utf8) else {
            return "{\"error\":\"native error\"}"
        }
        return str
    }

    // MARK: Injected JS shim

    /// Installed at document start so `window.HygurNative` exists before the
    /// React bundle evaluates. Each call returns a Promise keyed by an id the
    /// native side resolves via `window.__hygurResolve`.
    static let bridgeJS = """
    (function () {
      if (window.HygurNative) return;
      var pending = {}, seq = 0;
      window.__hygurResolve = function (id, ok, payload) {
        var p = pending[id];
        if (!p) return;
        delete pending[id];
        if (ok) p.resolve(payload);
        else p.reject(new Error((payload && payload.error) || "native error"));
      };
      function call(fn, args) {
        return new Promise(function (resolve, reject) {
          var id = ++seq;
          pending[id] = { resolve: resolve, reject: reject };
          try {
            window.webkit.messageHandlers.hygur.postMessage({ id: id, fn: fn, args: args || {} });
          } catch (e) {
            delete pending[id];
            reject(e);
          }
        });
      }
      // Live STT partials are pushed from Swift via this hook; the web app sets
      // HygurNative.onDictation to receive (text, isFinal) updates.
      window.__hygurDictation = function (text, isFinal) {
        if (window.HygurNative && typeof window.HygurNative.onDictation === "function") {
          window.HygurNative.onDictation(text, isFinal);
        }
      };
      window.HygurNative = {
        available: true,
        onDictation: null,
        ping: function () { return call("ping"); },
        calendar: {
          authorize: function () { return call("calendar.authorize"); },
          listCalendars: function () { return call("calendar.listCalendars"); },
          listEvents: function (hours) { return call("calendar.listEvents", { hours: hours || 168 }); },
          setEnabled: function (enabled, calendars) { return call("calendar.setEnabled", { enabled: !!enabled, calendars: calendars || [] }); }
        },
        dictation: {
          start: function () { return call("dictation.start"); },
          stop: function () { return call("dictation.stop"); }
        },
        prefs: {
          getBool: function (key) { return call("prefs.getBool", { key: key }); },
          setBool: function (key, value) { return call("prefs.setBool", { key: key, value: !!value }); }
        },
        perms: {
          status: function () { return call("perms.status"); }
        },
        openExternal: function (url) { return call("openExternal", { url: url }); },
        notify: function (title, body) { return call("notify", { title: title, body: body }); },
        download: function (filename, content) { return call("download", { filename: filename, content: content }); },
        print: function () { return call("print"); }
      };
    })();
    """
}
