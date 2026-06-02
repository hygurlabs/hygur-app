import AppKit

/// Minimal app delegate for window lifecycle. Hygur is a menu-bar app, so:
///  - closing the window must NOT quit the app, and
///  - clicking the Dock icon (or otherwise reopening) with no window open must
///    bring the window back — SwiftUI's default reopen doesn't fire reliably
///    once the single WindowGroup window has been torn down.
final class AppDelegate: NSObject, NSApplicationDelegate {
    func applicationShouldTerminateAfterLastWindowClosed(_ sender: NSApplication) -> Bool {
        false
    }

    func applicationShouldHandleReopen(_ sender: NSApplication, hasVisibleWindows flag: Bool) -> Bool {
        if !flag {
            // Delegate callbacks arrive on the main thread.
            MainActor.assumeIsolated { WindowAccess.shared.reveal() }
            return false // handled — don't let AppKit also reopen a window
        }
        return true
    }
}
