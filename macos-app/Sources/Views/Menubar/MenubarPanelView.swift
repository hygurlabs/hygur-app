import SwiftUI
import AppKit

/// `MenubarPanelView` is the panel rendered when the user clicks the
/// `MenubarExtra` icon. Shows current health, the last 8 events, and a row
/// of quick-action buttons (open main window, new note, search, run sync,
/// quit).
struct MenubarPanelView: View {
    @Environment(EventStreamService.self) private var events
    @Environment(SidecarSupervisor.self) private var supervisor
    @State private var isReconnecting: Bool = false

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            statusHeader
            Divider()
            askHygurField
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

    // MARK: - Header status mapping

    /// Mirror the menu bar icon status enum so the panel header and the
    /// glyph never disagree. Computed once per redraw — both tooltip and
    /// header subtitle pull from here.
    private var status: MenubarStatus {
        if !events.sidecarConnected { return .sidecarOffline }
        switch events.lmStudioStatus {
        case .up:      return .allOK
        case .down:    return .runtimeOffline
        case .unknown: return .runtimeUnknown
        }
    }

    // MARK: - Ask Hygur

    @State private var askText: String = ""

    /// Inline prompt for the menu bar — Send pushes the text into the chat
    /// input and brings the main window forward. Pre-Enter the user can
    /// edit before submission; we never auto-send because the menu bar is
    /// a "type fast and review" surface, not a one-shot terminal.
    private var askHygurField: some View {
        VStack(alignment: .leading, spacing: 6) {
            Text("Ask Hygur")
                .font(.caption.smallCaps())
                .foregroundStyle(.secondary)
                .padding(.horizontal, 12)
                .padding(.top, 6)
            HStack(spacing: 6) {
                TextField("What do you want to know?", text: $askText)
                    .textFieldStyle(.roundedBorder)
                    .onSubmit { submitAsk() }
                Button("Send") { submitAsk() }
                    .controlSize(.small)
                    .disabled(askText.trimmingCharacters(in: .whitespaces).isEmpty)
            }
            .padding(.horizontal, 12)
            .padding(.bottom, 6)
        }
    }

    private func submitAsk() {
        let trimmed = askText.trimmingCharacters(in: .whitespaces)
        guard !trimmed.isEmpty else { return }
        askText = ""
        // Reuse the same summon path as the global hotkey so both surfaces
        // produce identical state transitions (foreground + chat tab +
        // prefilled input).
        summonHygur(prefill: trimmed)
    }

    // MARK: - Header

    private var statusHeader: some View {
        VStack(alignment: .leading, spacing: 6) {
            HStack(spacing: 10) {
                Image(systemName: status.systemImage)
                    .foregroundStyle(status.color)
                    .font(.title3)
                VStack(alignment: .leading, spacing: 2) {
                    Text("Hygur").font(.headline)
                    Text(headerSubtitle)
                        .font(.caption)
                        .foregroundStyle(.secondary)
                }
                Spacer()
            }
            // Surface a Reconnect button only when the sidecar is the
            // problem — runtime issues are out of scope here (the user fixes
            // those in their vLLM/LM Studio process, not by tapping Hygur).
            if status == .sidecarOffline {
                Button {
                    reconnectSidecar()
                } label: {
                    HStack(spacing: 6) {
                        if isReconnecting {
                            ProgressView().controlSize(.small)
                        } else {
                            Image(systemName: "arrow.clockwise")
                        }
                        Text(isReconnecting ? "Reconnecting…" : "Reconnect now")
                            .font(.callout)
                    }
                }
                .buttonStyle(.bordered)
                .controlSize(.small)
                .disabled(isReconnecting)
            }
        }
        .padding(.horizontal, 12)
        .padding(.bottom, 8)
    }

    /// Header subtitle. Slightly more verbose than the menu bar tooltip
    /// because the panel has the room and the user clicked through to find
    /// out what's going on.
    private var headerSubtitle: String {
        switch status {
        case .allOK:           return "Sidecar OK · AI runtime connected"
        case .runtimeUnknown:  return "Sidecar OK · checking AI runtime…"
        case .runtimeOffline:  return "Sidecar OK · AI runtime offline"
        case .sidecarOffline:  return "Sidecar offline — retrying"
        }
    }

    /// Restart the bundled sidecar process. We don't try to fix runtime
    /// issues from here — that's an external process under the user's own
    /// control, and silently kicking it would surprise them.
    private func reconnectSidecar() {
        guard !isReconnecting else { return }
        isReconnecting = true
        Task {
            await supervisor.restart()
            // Give the SSE loop a moment to flip back to connected before we
            // re-enable the button — otherwise repeated taps spam restarts.
            try? await Task.sleep(nanoseconds: 1_500_000_000)
            isReconnecting = false
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
        case "sidecar_restart": return "arrow.clockwise.circle.fill"
        case "chat_failed": return "exclamationmark.bubble.fill"
        case "embedding_failed": return "waveform.slash"
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
            actionRow("Today's agenda", systemImage: "calendar") {
                openMainWindow()
                NotificationCenter.default.post(name: .navigateToSection, object: "chat")
                NotificationCenter.default.post(name: .openAgendaSheet, object: nil)
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
