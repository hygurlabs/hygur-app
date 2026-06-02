import AppKit
import Carbon.HIToolbox
import Foundation

/// Global hotkey registration via Carbon's Event Manager. We chose Carbon
/// over `NSEvent.addGlobalMonitorForEvents` because the latter requires the
/// app to be in the foreground (or hold Accessibility access) to receive
/// modifier-key combos — neither acceptable for system-wide shortcuts. Carbon
/// hotkeys work app-wide without entitlements as long as the app isn't
/// sandboxed (Hygur isn't).
///
/// Supports several independent hotkeys keyed by a small integer `id`
/// (1 = summon, 2 = quick-ask, …). A single shared Carbon event handler
/// dispatches each press to the matching Swift closure by reading the
/// `EventHotKeyID` off the event. Re-registering the same id replaces its
/// binding; `unregister(id:)` removes just that one.
@MainActor
final class HotkeyManager {
    static let shared = HotkeyManager()

    /// Stable signature disambiguating our hotkeys inside the shared
    /// application event target. The 4-char value is arbitrary.
    private static let signature: OSType = 0x48595352 // 'HYSR'

    private var eventHandler: EventHandlerRef?
    private var hotKeys: [UInt32: EventHotKeyRef] = [:] // id → ref
    private var handlers: [UInt32: () -> Void] = [:]    // id → handler

    private init() {}

    // No deinit: HotkeyManager is a singleton, so the only "teardown" is
    // process exit — Carbon releases the hotkeys on its own. A deinit would
    // also clash with Swift 6 strict concurrency (non-Sendable Carbon refs
    // can't be touched from a nonisolated deinit).

    /// Installs the shared Carbon event handler once. It routes each
    /// `kEventHotKeyPressed` to the registered closure for that hotkey id.
    private func ensureEventHandler() {
        guard eventHandler == nil else { return }
        let selfPtr = Unmanaged.passUnretained(self).toOpaque()
        var spec = EventTypeSpec(
            eventClass: OSType(kEventClassKeyboard),
            eventKind: UInt32(kEventHotKeyPressed)
        )
        InstallEventHandler(
            GetApplicationEventTarget(),
            { _, event, userData -> OSStatus in
                guard let userData, let event else { return noErr }
                var hkID = EventHotKeyID()
                let status = GetEventParameter(
                    event,
                    EventParamName(kEventParamDirectObject),
                    EventParamType(typeEventHotKeyID),
                    nil,
                    MemoryLayout<EventHotKeyID>.size,
                    nil,
                    &hkID
                )
                guard status == noErr else { return noErr }
                let manager = Unmanaged<HotkeyManager>
                    .fromOpaque(userData)
                    .takeUnretainedValue()
                let id = hkID.id
                // Carbon delivers on the main thread already; the explicit
                // hop keeps the call site `@MainActor`-clean.
                DispatchQueue.main.async { manager.handlers[id]?() }
                return noErr
            },
            1,
            &spec,
            selfPtr,
            &eventHandler
        )
    }

    /// Registers (or replaces) a global hotkey. `keyCode` is a Carbon virtual
    /// key code (e.g. `kVK_ANSI_H`); `modifiers` is the Carbon modifier mask
    /// (`cmdKey | shiftKey`, NOT `NSEvent.ModifierFlags`). `handler` runs on
    /// the main actor on every press.
    func register(
        id: UInt32 = 1,
        keyCode: UInt32,
        modifiers: UInt32,
        handler: @escaping () -> Void
    ) {
        unregister(id: id)
        ensureEventHandler()
        handlers[id] = handler

        var ref: EventHotKeyRef?
        let hotKeyID = EventHotKeyID(signature: Self.signature, id: id)
        let status = RegisterEventHotKey(
            keyCode,
            modifiers,
            hotKeyID,
            GetApplicationEventTarget(),
            0,
            &ref
        )
        if status == noErr {
            hotKeys[id] = ref
        } else {
            NSLog("HotkeyManager: RegisterEventHotKey(id: \(id)) failed (\(status)) — likely taken by another app")
            handlers[id] = nil
        }
    }

    /// Removes one hotkey binding. Safe to call when nothing is registered.
    func unregister(id: UInt32 = 1) {
        if let ref = hotKeys[id] {
            UnregisterEventHotKey(ref)
            hotKeys[id] = nil
        }
        handlers[id] = nil
    }

    // MARK: - Shortcut constants

    /// Summon (bring the window forward, focus Ask). Default Cmd+Shift+H.
    static var defaultKeyCode: UInt32 { UInt32(kVK_ANSI_H) }
    static var defaultModifiers: UInt32 { UInt32(cmdKey | shiftKey) }

    /// Quick-ask floating palette. Cmd+Shift+K.
    static let quickAskID: UInt32 = 2
    static var quickAskKeyCode: UInt32 { UInt32(kVK_ANSI_K) }
    static var quickAskModifiers: UInt32 { UInt32(cmdKey | shiftKey) }
}
