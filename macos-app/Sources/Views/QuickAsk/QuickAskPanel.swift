import AppKit
import SwiftUI

/// A Spotlight-style floating field, summoned anywhere by the global
/// Cmd+Shift+K hotkey, that lets the user write to Hygur without opening the
/// main window. On submit it calls `summonHygur(prefill:)`, which brings the
/// WebUI forward and deep-links the query into the Ask view.
@MainActor
final class QuickAskController {
    static let shared = QuickAskController()
    private var panel: NSPanel?
    private init() {}

    /// Toggles visibility — press once to open, again (or Escape / click-away)
    /// to dismiss.
    func toggle() {
        if let panel, panel.isVisible {
            panel.orderOut(nil)
        } else {
            present()
        }
    }

    func dismiss() {
        panel?.orderOut(nil)
    }

    private func present() {
        let panel = self.panel ?? makePanel()
        self.panel = panel
        positionTopCenter(panel)
        NSApp.activate(ignoringOtherApps: true)
        panel.makeKeyAndOrderFront(nil)
    }

    private func positionTopCenter(_ panel: NSPanel) {
        guard let screen = NSScreen.main else { return }
        let visible = screen.visibleFrame
        let size = panel.frame.size
        panel.setFrameOrigin(
            NSPoint(
                x: visible.midX - size.width / 2,
                y: visible.maxY - size.height - 160
            )
        )
    }

    private func makePanel() -> NSPanel {
        let rect = NSRect(x: 0, y: 0, width: 620, height: 84)
        let panel = KeyableQuickAskPanel(
            contentRect: rect,
            styleMask: [.borderless],
            backing: .buffered,
            defer: false
        )
        panel.level = .floating
        panel.isOpaque = false
        panel.backgroundColor = .clear
        panel.hasShadow = true
        panel.isMovableByWindowBackground = false
        panel.hidesOnDeactivate = true
        panel.collectionBehavior = [.canJoinAllSpaces, .fullScreenAuxiliary]
        panel.animationBehavior = .utilityWindow

        let root = QuickAskView(
            onSubmit: { [weak self] text in
                self?.dismiss()
                summonHygur(prefill: text)
            },
            onCancel: { [weak self] in self?.dismiss() }
        )
        let hosting = NSHostingView(rootView: root)
        hosting.frame = rect
        hosting.autoresizingMask = [.width, .height]
        panel.contentView = hosting
        return panel
    }
}

/// Borderless panels don't accept key focus by default; override so the text
/// field can become first responder and receive typing.
private final class KeyableQuickAskPanel: NSPanel {
    override var canBecomeKey: Bool { true }
    override var canBecomeMain: Bool { true }
}

private struct QuickAskView: View {
    let onSubmit: (String) -> Void
    let onCancel: () -> Void

    @State private var text = ""
    @FocusState private var focused: Bool

    var body: some View {
        HStack(spacing: 13) {
            Image(systemName: "sparkles")
                .font(.system(size: 17))
                .foregroundStyle(.secondary)

            TextField("Ask Hygur…", text: $text)
                .textFieldStyle(.plain)
                .font(.system(size: 19, weight: .regular))
                .focused($focused)
                .onSubmit(submit)
        }
        .padding(.horizontal, 22)
        .frame(height: 64)
        .background(
            .regularMaterial,
            in: RoundedRectangle(cornerRadius: 16, style: .continuous)
        )
        .overlay(
            RoundedRectangle(cornerRadius: 16, style: .continuous)
                .strokeBorder(Color.primary.opacity(0.08))
        )
        .padding(10) // breathing room for the panel's drop shadow
        .onAppear {
            // Defer one runloop tick so the panel is key before we focus.
            DispatchQueue.main.async { focused = true }
        }
        .onExitCommand(perform: onCancel)
    }

    private func submit() {
        let trimmed = text.trimmingCharacters(in: .whitespacesAndNewlines)
        text = ""
        guard !trimmed.isEmpty else {
            onCancel()
            return
        }
        onSubmit(trimmed)
    }
}
