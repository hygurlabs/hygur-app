import SwiftUI
import AppKit

/// `MenubarPanelView` is the panel rendered when the user clicks the
/// `MenubarExtra` icon. Shows current health, the last 8 events, and a row
/// of quick-action buttons (open main window, new note, search, run sync,
/// quit).
struct MenubarPanelView: View {
    @Environment(EventStreamService.self) private var events

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            statusHeader
            Divider()
            eventsSection
            Divider()
            quickActions
            Divider()
            footer
        }
        .padding(.vertical, 8)
        .frame(width: 320)
        .onChange(of: events.recentEvents.count) { _, _ in
            handleEventStreamUpdate()
        }
    }

    // MARK: - Header

    private var statusHeader: some View {
        HStack(spacing: 10) {
            Image(systemName: "circle.fill")
                .foregroundStyle(headerColor)
                .font(.title3)
            VStack(alignment: .leading, spacing: 2) {
                Text("Hygur").font(.headline)
                Text(headerSubtitle)
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }
            Spacer()
        }
        .padding(.horizontal, 12)
        .padding(.bottom, 8)
    }

    private var headerColor: Color {
        if !events.sidecarConnected { return HygurColors.danger }
        switch events.lmStudioStatus {
        case .up: return HygurColors.success
        case .down: return HygurColors.danger
        case .unknown: return HygurColors.warning
        }
    }

    private var headerSubtitle: String {
        if !events.sidecarConnected { return "Sidecar offline" }
        switch events.lmStudioStatus {
        case .up: return "Sidecar OK · LM Studio connected"
        case .down: return "Sidecar OK · LM Studio offline"
        case .unknown: return "Sidecar OK · checking LM Studio…"
        }
    }

    // MARK: - Recent events

    private var eventsSection: some View {
        VStack(alignment: .leading, spacing: 0) {
            Text("Recent activity")
                .font(.caption.smallCaps())
                .foregroundStyle(.secondary)
                .padding(.horizontal, 12)
                .padding(.vertical, 6)

            if events.recentEvents.isEmpty {
                Text("No recent events")
                    .font(.caption)
                    .foregroundStyle(.tertiary)
                    .padding(.horizontal, 12)
                    .padding(.bottom, 8)
            } else {
                ForEach(events.recentEvents.prefix(8)) { evt in
                    HStack(alignment: .top, spacing: 8) {
                        Image(systemName: iconName(for: evt))
                            .foregroundStyle(.tint)
                            .frame(width: 18)
                            .padding(.top, 2)
                        VStack(alignment: .leading, spacing: 1) {
                            Text(evt.title)
                                .font(.callout)
                                .lineLimit(1)
                            RelativeTimeText(date: evt.receivedAt)
                                .font(.caption2)
                                .foregroundStyle(.secondary)
                        }
                        Spacer()
                    }
                    .padding(.horizontal, 12)
                    .padding(.vertical, 4)
                }
            }
        }
    }

    private func iconName(for evt: ActivityEvent) -> String {
        switch evt.type {
        case "brief": return "doc.text.below.ecg"
        case "priority_mail": return "envelope.badge.fill"
        case "lm_studio": return "cpu"
        case "connectors": return "puzzlepiece.extension"
        case "ingest": return "tray.and.arrow.down"
        default: return "circle.fill"
        }
    }

    // MARK: - Quick actions

    @State private var briefRunning = false
    @State private var briefError: String?
    /// Wall-clock at which the last brief was requested. The button stays
    /// disabled until a `brief` SSE event with `receivedAt > briefRequestedAt`
    /// arrives, or until the watchdog (90s) fires — whichever comes first.
    @State private var briefRequestedAt: Date?

    private var quickActions: some View {
        VStack(alignment: .leading, spacing: 0) {
            Text("Quick actions")
                .font(.caption.smallCaps())
                .foregroundStyle(.secondary)
                .padding(.horizontal, 12)
                .padding(.vertical, 6)

            actionRow("Open Hygur", systemImage: "arrow.up.right.square") {
                openMainWindow()
            }
            actionRow("New note", systemImage: "square.and.pencil") {
                openMainWindow()
                NotificationCenter.default.post(name: .showNewNoteSheet, object: nil)
            }
            actionRow("Search", systemImage: "magnifyingglass") {
                openMainWindow()
                NotificationCenter.default.post(name: .navigateToSection, object: "search")
            }
            actionRow("Activity", systemImage: "bell.badge") {
                openMainWindow()
                NotificationCenter.default.post(name: .navigateToSection, object: "activity")
            }

            // Run brief now — fires the daily brief on demand. The UI shows
            // a spinner until the sidecar acknowledges (200 OK with "queued");
            // the actual brief content lands as a `brief` event in Activity
            // 10-30 s later.
            briefActionRow

            if let err = briefError {
                Text(err)
                    .font(.caption2)
                    .foregroundStyle(HygurColors.danger)
                    .padding(.horizontal, 12)
                    .padding(.bottom, 4)
            }
        }
    }

    private var briefActionRow: some View {
        Button {
            triggerBrief()
        } label: {
            HStack(spacing: 8) {
                if briefRunning {
                    ProgressView().controlSize(.small).frame(width: 18)
                } else {
                    Image(systemName: "doc.text.below.ecg").frame(width: 18)
                }
                Text(briefRunning ? "Running brief…" : "Run brief now")
                    .font(.callout)
                Spacer()
            }
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .disabled(briefRunning)
        .padding(.horizontal, 12)
        .padding(.vertical, 5)
    }

    private func triggerBrief() {
        guard !briefRunning else { return }
        briefRunning = true
        briefError = nil
        let requestedAt = Date()
        briefRequestedAt = requestedAt
        Task {
            do {
                _ = try await SidecarService.fromSettings().runBrief()
                briefError = nil
                // Surface the Activity panel so the user can watch the
                // resulting `brief` event arrive.
                openMainWindow()
                NotificationCenter.default.post(name: .navigateToSection, object: "activity")
                // Watchdog: the brief usually lands in 10-30 s; if the SSE
                // event never reaches us (network drop, sidecar restart),
                // unfreeze the button after 90 s so the user isn't stuck.
                try? await Task.sleep(nanoseconds: 90 * 1_000_000_000)
                if briefRequestedAt == requestedAt {
                    briefRunning = false
                    briefRequestedAt = nil
                }
            } catch SidecarError.serviceUnavailable {
                briefError = "Brief disabled — set daily_brief.enabled=true"
                briefRunning = false
                briefRequestedAt = nil
            } catch {
                briefError = "Failed: \(error.localizedDescription)"
                briefRunning = false
                briefRequestedAt = nil
            }
        }
    }

    /// Clears `briefRunning` as soon as a `brief` event arrives that was
    /// produced after the user's last click — this is the happy path that
    /// short-circuits the 90 s watchdog.
    private func handleEventStreamUpdate() {
        guard let requestedAt = briefRequestedAt else { return }
        let landed = events.recentEvents.contains { evt in
            evt.type == "brief" && evt.receivedAt >= requestedAt
        }
        if landed {
            briefRunning = false
            briefRequestedAt = nil
        }
    }

    private func actionRow(_ label: String, systemImage: String, action: @escaping () -> Void) -> some View {
        Button(action: action) {
            HStack(spacing: 8) {
                Image(systemName: systemImage)
                    .frame(width: 18)
                Text(label)
                    .font(.callout)
                Spacer()
            }
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .padding(.horizontal, 12)
        .padding(.vertical, 5)
    }

    // MARK: - Footer

    private var footer: some View {
        HStack {
            Spacer()
            Button("Quit Hygur") {
                NSApplication.shared.terminate(nil)
            }
            .buttonStyle(.plain)
            .font(.caption)
            .foregroundStyle(.secondary)
            .padding(.horizontal, 12)
            .padding(.top, 6)
        }
    }

    // MARK: - Helpers

    private func openMainWindow() {
        NSApp.activate(ignoringOtherApps: true)
        // Find an existing window or open via app re-activation.
        for window in NSApp.windows where window.canBecomeMain {
            window.makeKeyAndOrderFront(nil)
            return
        }
    }
}
