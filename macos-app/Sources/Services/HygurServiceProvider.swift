import AppKit
import Foundation
import UserNotifications

/// Backs the `Add to Hygur` entry in the system Services menu (declared in
/// `Sources/Info.plist` under `NSServices`). When the user invokes the
/// service from any text-aware app (TextEdit, Safari address bar, Mail, …),
/// AppKit calls `addToHygur(_:userData:error:)` on this object with the
/// current pasteboard contents.
///
/// The provider is intentionally minimal: it grabs URL or text off the
/// pasteboard, posts to the sidecar's `/notes` endpoint (the same one the
/// in-app "New Note" flow uses), and surfaces success/failure via a native
/// notification. No UI, no chrome — the Services menu is for one-shot
/// captures; users who want a confirmation panel use the Share Extension.
@MainActor
@objc(HygurServiceProvider)
final class HygurServiceProvider: NSObject {
    /// Selector invoked by AppKit when the user picks "Add to Hygur" from the
    /// Services menu. Signature is dictated by the platform — do not rename.
    /// See: https://developer.apple.com/library/archive/documentation/Cocoa/Conceptual/SysServices/
    @objc func addToHygur(
        _ pasteboard: NSPasteboard,
        userData: String,
        error: AutoreleasingUnsafeMutablePointer<NSString>
    ) {
        // Try URL first (Safari address-bar selection lands here as a URL),
        // then fall back to plain text. We deliberately don't union the two
        // — a selection is either one or the other from the user's POV.
        let captured: CapturedItem
        if let urlString = pasteboard.string(forType: .URL) ?? pasteboard.string(forType: .fileURL),
           let url = URL(string: urlString) {
            captured = .url(url)
        } else if let text = pasteboard.string(forType: .string), !text.isEmpty {
            captured = .text(text)
        } else {
            error.pointee = "No URL or text on the pasteboard." as NSString
            postFailureNotification(reason: "Nothing to capture from the selection.")
            return
        }

        // Hop off the main actor for the network round-trip, then bounce
        // back to post the notification. We don't block the Services menu
        // invocation on the network — AppKit considers the service complete
        // as soon as this method returns.
        Task.detached { [captured] in
            do {
                try await SharedSidecarClient.ingest(captured)
                await MainActor.run {
                    HygurServiceProvider.postSuccessNotification(for: captured)
                }
            } catch {
                await MainActor.run {
                    HygurServiceProvider.postFailureNotificationFromError(error)
                }
            }
        }
    }

    // MARK: - Notifications

    /// Use the same `UNUserNotificationCenter` the rest of the app already
    /// requested authorization for — the Services menu shouldn't be the
    /// thing that prompts for notification access. If the user never
    /// granted it the post just fails silently.
    private static func postSuccessNotification(for item: CapturedItem) {
        let content = UNMutableNotificationContent()
        content.title = "Saved to Hygur"
        switch item {
        case .url(let url):
            content.body = url.absoluteString
        case .text(let text):
            content.body = String(text.prefix(140))
        }
        content.sound = .default

        let req = UNNotificationRequest(
            identifier: "hygur-services-\(UUID().uuidString)",
            content: content,
            trigger: nil
        )
        UNUserNotificationCenter.current().add(req)
    }

    private static func postFailureNotificationFromError(_ error: Error) {
        let reason: String
        if let sidecarError = error as? SharedSidecarClient.IngestError {
            reason = sidecarError.userFacingMessage
        } else {
            reason = error.localizedDescription
        }
        postFailureNotification(reason: reason)
    }

    private func postFailureNotification(reason: String) {
        Self.postFailureNotification(reason: reason)
    }

    private static func postFailureNotification(reason: String) {
        let content = UNMutableNotificationContent()
        content.title = "Couldn't add to Hygur"
        content.body = reason
        content.sound = .default
        let req = UNNotificationRequest(
            identifier: "hygur-services-error-\(UUID().uuidString)",
            content: content,
            trigger: nil
        )
        UNUserNotificationCenter.current().add(req)
    }
}

/// Tiny domain enum so the provider can pattern-match on what the user is
/// capturing without sprinkling string/URL checks throughout. Mirrored on
/// the Share Extension side — kept here as a separate type to avoid
/// pulling AppKit into the extension's tight UI module.
enum CapturedItem: Sendable {
    case url(URL)
    case text(String)
}
