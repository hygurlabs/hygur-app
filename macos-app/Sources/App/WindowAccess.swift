import AppKit
import SwiftUI

/// Centralises "bring the main Hygur window to the front, recreating it if it
/// was closed." Both the summon hotkey and the menu-bar route through this.
///
/// A plain `makeKeyAndOrderFront` over `NSApp.windows` fails once SwiftUI tears
/// the window down on close — which is exactly when the user wants it back.
/// So we keep a reference to SwiftUI's `openWindow` action (captured by the
/// window's view) and fall back to it when no live window remains.
@MainActor
final class WindowAccess {
    static let shared = WindowAccess()
    private init() {}

    /// Installed by the main window's view via `@Environment(\.openWindow)`.
    /// Reopens a fresh "main" window when none is currently alive.
    var openMainWindow: (() -> Void)?

    /// Brings the main window forward, opening a new one if it was closed.
    func reveal() {
        NSApp.activate(ignoringOtherApps: true)
        // The main window is a plain NSWindow; exclude panels (the menu-bar
        // popover and the quick-ask palette both report canBecomeMain).
        if let window = NSApp.windows.first(where: { !($0 is NSPanel) && $0.canBecomeMain }) {
            window.makeKeyAndOrderFront(nil)
        } else {
            openMainWindow?()
        }
    }
}
