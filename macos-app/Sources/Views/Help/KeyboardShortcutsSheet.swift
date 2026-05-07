import SwiftUI

/// Compact reference of every keyboard shortcut Hygur registers. Surfaced
/// from Help → Keyboard Shortcuts. Static content — kept in lockstep with
/// `HygurApp.commands` and `HotkeyManager.defaultKeyCode/Modifiers`. If a
/// shortcut moves, this file moves with it.
struct KeyboardShortcutsSheet: View {
    @Environment(\.dismiss) private var dismiss

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            header
            Divider()
            ScrollView {
                VStack(alignment: .leading, spacing: 24) {
                    section("Global", rows: globalShortcuts)
                    section("Navigation", rows: navigationShortcuts)
                    section("Chat", rows: chatShortcuts)
                }
                .padding(20)
            }
            Divider()
            footer
        }
        .frame(width: 480, height: 520)
    }

    private var header: some View {
        HStack(spacing: 10) {
            Image(systemName: "keyboard")
                .font(.title2)
                .foregroundStyle(.tint)
            VStack(alignment: .leading, spacing: 2) {
                Text("Keyboard shortcuts").font(.headline)
                Text("Quick reference for everything Hygur listens to")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }
            Spacer()
        }
        .padding(16)
    }

    private var footer: some View {
        HStack {
            Spacer()
            Button("Done") { dismiss() }
                .keyboardShortcut(.defaultAction)
        }
        .padding(12)
    }

    private func section(_ title: String, rows: [Row]) -> some View {
        VStack(alignment: .leading, spacing: 8) {
            Text(title.uppercased())
                .font(.caption.smallCaps())
                .foregroundStyle(.secondary)
            ForEach(rows) { row in
                HStack(alignment: .firstTextBaseline) {
                    Text(row.label)
                        .font(.callout)
                    Spacer()
                    Text(row.keys)
                        .font(.system(.callout, design: .monospaced))
                        .foregroundStyle(.secondary)
                        .padding(.horizontal, 8)
                        .padding(.vertical, 3)
                        .background(
                            RoundedRectangle(cornerRadius: 5, style: .continuous)
                                .fill(Color.secondary.opacity(0.1))
                        )
                }
            }
        }
    }

    private struct Row: Identifiable {
        let label: String
        let keys: String
        var id: String { label }
    }

    private var globalShortcuts: [Row] {
        [
            Row(label: "Summon Hygur (system-wide)", keys: "⇧⌘H"),
            Row(label: "Command palette", keys: "⌘K"),
            Row(label: "New note", keys: "⌘N"),
            Row(label: "Search", keys: "⌘F"),
        ]
    }

    private var navigationShortcuts: [Row] {
        [
            Row(label: "Chat", keys: "⌘1"),
            Row(label: "Knowledge base", keys: "⌘2"),
            Row(label: "Notes", keys: "⌘3"),
            Row(label: "Timeline", keys: "⌘4"),
            Row(label: "Projects", keys: "⌘5"),
            Row(label: "Connectors", keys: "⌘6"),
        ]
    }

    private var chatShortcuts: [Row] {
        [
            Row(label: "Copy last response", keys: "⇧⌘C"),
            Row(label: "Send message", keys: "↩"),
            Row(label: "Insert newline in input", keys: "⇧↩"),
        ]
    }
}

#Preview {
    KeyboardShortcutsSheet()
}
