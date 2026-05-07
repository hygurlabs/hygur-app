import SwiftUI

@main
struct HygurApp: App {
    @AppStorage("lastSidebarSelection") private var lastSidebarSelectionRaw: String = "chat"

    // Gates the first-run onboarding sheet. Flipped to `true` when the user
    // either finishes the flow or chooses Start chatting on the final step.
    @AppStorage("onboarding.completed") private var onboardingCompleted: Bool = false

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

    // Local favorites store — backs the new "Favorites" sidebar section
    // until the sidecar gains a real `is_favorite` column.
    @State private var favoritesStore = FavoritesStore()

    // Cross-view selection that drives the right-hand Properties panel.
    // Single-click a note / KB item / mail thread → entity flows here.
    @State private var inspectorSelection = InspectorSelection()

    // App-level singleton. Per-ChatView ownership raced because the
    // sidebar swap (.newChat ↔ .chatSession) creates a fresh ChatView,
    // and SFSpeechRecognizer's first instantiation on the user-tap
    // frame triggered a window-level layout reflow on macOS 26.
    // Pre-warm once at launch, before any ChatView appears.
    @State private var voiceService = VoiceService()

    @State private var showOnboarding: Bool = false

    var body: some Scene {
        WindowGroup {
            ContentView()
                .environment(eventStream)
                .environment(supervisor)
                .environment(updater)
                .environment(favoritesStore)
                .environment(inspectorSelection)
                .environment(voiceService)
                .sheet(isPresented: $showOnboarding) {
                    OnboardingView(onComplete: {
                        onboardingCompleted = true
                        showOnboarding = false
                    })
                    // Sheets present a fresh environment scope on macOS 26;
                    // re-inject the @Observable services the steps need so
                    // `@Environment(SidecarSupervisor.self)` resolves inside
                    // the sheet (otherwise the Connect AI model step crashes
                    // on transition).
                    .environment(eventStream)
                    .environment(supervisor)
                    .environment(updater)
                    .interactiveDismissDisabled()
                }
                .onAppear {
                    // Surface the onboarding sheet on the very first launch.
                    // Subsequent launches see `onboardingCompleted == true`
                    // and the sheet stays dismissed.
                    if !onboardingCompleted {
                        showOnboarding = true
                    }
                }
                .onOpenURL { url in
                    handleHygurURL(url)
                }
                .task {
                    // Pre-warm the speech recognizer in the background so the
                    // first user-tap on the mic doesn't synchronously load
                    // the Speech framework on the tap frame (which on
                    // macOS 26 cascades into a window-level layout reflow).
                    Task { await voiceService.prepare() }

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
                #if DEBUG
                Divider()
                Button("Reset Onboarding…") {
                    onboardingCompleted = false
                    showOnboarding = true
                }
                #endif
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

// MARK: - URL Routing

/// Routes `hygur://session/<uuid>` and `hygur://note/<id>` deep links —
/// fired by Spotlight results and (later) third-party links/Shortcuts.
/// Sessions get full deep-linking via the existing `.openChatSession`
/// notification; notes navigate to the Notes tab for now (the sheet to
/// open a specific note is a follow-up).
private func handleHygurURL(_ url: URL) {
    guard url.scheme == "hygur" else { return }
    let host = url.host()
    let segment = url.pathComponents.dropFirst().first ?? ""
    switch host {
    case "session":
        guard !segment.isEmpty else { return }
        NotificationCenter.default.post(name: .openChatSession, object: segment)
    case "note":
        // No per-note open path yet — land the user on Notes so they can
        // pick the result manually.
        NotificationCenter.default.post(name: .navigateToSection, object: "notes")
    default:
        break
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
    /// Opens the Settings window and focuses the "About" tab where the
    /// update controls live. Posted by the "Check for Updates…" menu item.
    static let openUpdatesPane = Notification.Name("openUpdatesPane")
}
