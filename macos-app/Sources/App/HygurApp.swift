import SwiftUI

@main
struct HygurApp: App {
    @AppStorage("lastSidebarSelection") private var lastSidebarSelectionRaw: String = "chat"

    // Single, app-wide event stream consumer. Connects to /events on the
    // sidecar at launch and fans events out to the menubar, ActivityView,
    // and (later) the notifications service.
    @State private var eventStream = EventStreamService()

    // Sidecar process supervisor — started lazily on first window appearance.
    // Idempotent; a no-op if the user runs the sidecar manually elsewhere.
    @State private var supervisor = SidecarSupervisor()

    // GitHub-backed update checker. Owns the auto-check schedule and the
    // download/install state machine.
    @State private var updater = Updater()

    var body: some Scene {
        WindowGroup {
            ContentView()
                .environment(eventStream)
                .environment(supervisor)
                .environment(updater)
                .task {
                    // Spawn the supervised sidecar child process if the binary
                    // is installed. Errors are surfaced via `supervisor.lastError`
                    // in the Settings view; the rest of the app continues to
                    // work against a remote / manually-launched sidecar.
                    supervisor.start()

                    // Wait for the sidecar HTTP server to be ready before
                    // pushing secrets — supervisor.start() is non-blocking and
                    // the process needs time to bind its port.
                    await waitForSidecar()

                    // Push Keychain-stored secrets to the sidecar at launch so
                    // it can re-init enabled connectors with their credentials.
                    await ConnectorsViewModel().pushAllSecretsToSidecar()

                    // Bridge events → native notifications. The
                    // NotificationsService consults the user's opt-in
                    // toggles before actually posting anything.
                    eventStream.onEvent = { event in
                        NotificationsService.shared.handle(event)
                    }
                    // Start the event consumer once the sidecar URL is known.
                    eventStream.start(sidecar: SidecarService.fromSettings())

                    // Background update check (no-op if checked in last 24h or
                    // if the user disabled auto-check).
                    await updater.checkAtLaunchIfDue()
                }
        }
        .windowStyle(.hiddenTitleBar)
        .commands {
            CommandGroup(replacing: .newItem) {
                Button("New Note") {
                    NotificationCenter.default.post(name: .showNewNoteSheet, object: nil)
                }
                .keyboardShortcut("n", modifiers: .command)
            }

            CommandGroup(after: .newItem) {
                Divider()
                Button("Command Palette…") {
                    NotificationCenter.default.post(name: .showCommandPalette, object: nil)
                }
                .keyboardShortcut("k", modifiers: .command)
            }

            CommandMenu("Navigate") {
                Button("Chat") {
                    NotificationCenter.default.post(name: .navigateToSection, object: "chat")
                }
                .keyboardShortcut("1", modifiers: .command)

                Button("Knowledge Base") {
                    NotificationCenter.default.post(name: .navigateToSection, object: "knowledgeBase")
                }
                .keyboardShortcut("2", modifiers: .command)

                Button("Notes") {
                    NotificationCenter.default.post(name: .navigateToSection, object: "notes")
                }
                .keyboardShortcut("3", modifiers: .command)

                Button("Timeline") {
                    NotificationCenter.default.post(name: .navigateToSection, object: "graph")
                }
                .keyboardShortcut("4", modifiers: .command)

                Button("Projects") {
                    NotificationCenter.default.post(name: .navigateToSection, object: "projects")
                }
                .keyboardShortcut("5", modifiers: .command)

                Button("Connectors") {
                    NotificationCenter.default.post(name: .navigateToSection, object: "connectors")
                }
                .keyboardShortcut("6", modifiers: .command)

                Divider()

                Button("Search") {
                    NotificationCenter.default.post(name: .navigateToSection, object: "search")
                }
                .keyboardShortcut("f", modifiers: .command)
            }

            CommandGroup(after: .appInfo) {
                Button("Check for Updates…") {
                    NotificationCenter.default.post(name: .openUpdatesPane, object: nil)
                }
            }
        }

        Settings {
            SettingsView()
                .environment(supervisor)
                .environment(updater)
        }

        // Menubar status — always visible. Drives a small status dot
        // (green/orange/red) reflecting sidecar + LM Studio reachability,
        // with a click-to-reveal panel showing recent events and quick actions.
        MenuBarExtra {
            MenubarPanelView()
                .environment(eventStream)
                .environment(supervisor)
        } label: {
            MenubarStatusIcon()
                .environment(eventStream)
        }
        .menuBarExtraStyle(.window)
    }
}

// MARK: - Sidecar Readiness

/// Polls `/health` until the sidecar responds or the attempt budget is exhausted.
/// `maxAttempts * delay` ≈ 6 s total wait at defaults — enough for a cold start on
/// a mid-range Mac while keeping the UI responsive (each sleep is 300 ms).
private func waitForSidecar(maxAttempts: Int = 20, delay: UInt64 = 300_000_000) async {
    let sidecar = SidecarService.fromSettings()
    for _ in 0..<maxAttempts {
        if (try? await sidecar.health()) != nil { return }
        try? await Task.sleep(nanoseconds: delay) // 300 ms between attempts
    }
}

// MARK: - Notification Names

extension Notification.Name {
    static let showNewNoteSheet = Notification.Name("showNewNoteSheet")
    static let navigateToSection = Notification.Name("navigateToSection")
    static let showCommandPalette = Notification.Name("showCommandPalette")
    /// Opens the QuickLook preview for a specific knowledge content ID.
    /// Posted with `object` = content ID (`String`).
    static let openDocument = Notification.Name("openDocument")
    /// Switches the chat detail view to a specific session.
    /// Posted with `object` = session UUID string.
    static let openChatSession = Notification.Name("openChatSession")
    /// Opens the Settings window and focuses the "À propos" tab where the
    /// update controls live. Posted by the "Check for Updates…" menu item.
    static let openUpdatesPane = Notification.Name("openUpdatesPane")
}
