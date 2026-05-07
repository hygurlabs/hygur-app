import AppKit
import Carbon.HIToolbox
import Foundation

/// Global hotkey registration via Carbon's Event Manager. We chose Carbon
/// over `NSEvent.addGlobalMonitorForEvents` because the latter requires the
/// app to be in the foreground (or hold Accessibility access) to receive
/// modifier-key combos — neither acceptable for a system-wide "summon Hygur"
/// shortcut. Carbon hotkeys work app-wide without entitlements as long as
/// the app isn't sandboxed (Hygur isn't).
///
/// Usage: `HotkeyManager.shared.register(handler: ...)` once at launch with
/// the user's preferred shortcut. Call `unregister()` before re-registering
/// with a different shortcut (otherwise both fire).
@MainActor
final class HotkeyManager {
    static let shared = HotkeyManager()

    /// Stable signature used to disambiguate our hotkey ID inside the
    /// shared application event target. The 4-char value is arbitrary; just
    /// must not clash with another registered hotkey.
    private static let signature: OSType = 0x48594752 // 'HYGR'

    private var hotKeyRef: EventHotKeyRef?
    private var eventHandler: EventHandlerRef?
    private var onTrigger: (() -> Void)?

    private init() {}

    // No deinit: HotkeyManager is a singleton, so the only "teardown" is
    // process exit — at which point Carbon releases the hotkey on its own.
    // Adding a deinit would also clash with Swift 6 strict concurrency
    // (non-Sendable Carbon refs cannot be touched from a nonisolated deinit).

    /// Registers a global hotkey. `keyCode` is a Carbon virtual key code
    /// (e.g. `kVK_ANSI_H`); `modifiers` is the Carbon modifier mask
    /// (`cmdKey | shiftKey` etc., NOT NSEvent.ModifierFlags). `handler` is
    /// invoked on the main actor every time the user hits the combo.
    /// Re-registering replaces any previous binding.
    func register(
        keyCode: UInt32,
        modifiers: UInt32,
        handler: @escaping () -> Void
    ) {
        unregister()
        onTrigger = handler

        // Carbon dispatch is C-style; pass `self` as opaque user data so
        // the handler can route the event back to this Swift instance.
        let selfPtr = Unmanaged.passUnretained(self).toOpaque()
        var spec = EventTypeSpec(
            eventClass: OSType(kEventClassKeyboard),
            eventKind: UInt32(kEventHotKeyPressed)
        )
        let installStatus = InstallEventHandler(
            GetApplicationEventTarget(),
            { _, _, userData -> OSStatus in
                guard let userData else { return noErr }
                let manager = Unmanaged<HotkeyManager>
                    .fromOpaque(userData)
                    .takeUnretainedValue()
                // Hop to main — Carbon delivers the event on the main thread
                // already, but `onTrigger` is `@MainActor`-isolated and we
                // want explicit safety here.
                DispatchQueue.main.async { manager.onTrigger?() }
                return noErr
            },
            1,
            &spec,
            selfPtr,
            &eventHandler
        )
        guard installStatus == noErr else {
            NSLog("HotkeyManager: InstallEventHandler failed (\(installStatus))")
            return
        }

        let id = EventHotKeyID(signature: Self.signature, id: 1)
        let registerStatus = RegisterEventHotKey(
            keyCode,
            modifiers,
            id,
            GetApplicationEventTarget(),
            0,
            &hotKeyRef
        )
        if registerStatus != noErr {
            NSLog("HotkeyManager: RegisterEventHotKey failed (\(registerStatus)) — likely already taken by another app")
        }
    }

    /// Removes the current hotkey binding. Safe to call when nothing is
    /// registered. Always called by `register()` before installing a new
    /// handler so duplicate keystrokes never fire.
    func unregister() {
        if let ref = hotKeyRef {
            UnregisterEventHotKey(ref)
            hotKeyRef = nil
        }
        if let handler = eventHandler {
            RemoveEventHandler(handler)
            eventHandler = nil
        }
        onTrigger = nil
    }

    /// Convenience binding for the default summon shortcut (Cmd+Shift+H).
    /// Public so Settings can rebind without re-deriving the constants.
    static var defaultKeyCode: UInt32 { UInt32(kVK_ANSI_H) }
    static var defaultModifiers: UInt32 { UInt32(cmdKey | shiftKey) }
}
