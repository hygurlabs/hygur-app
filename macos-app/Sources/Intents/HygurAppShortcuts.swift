import AppIntents

/// Surfaces all Hygur App Intents in the Shortcuts app and Siri without
/// requiring the user to add them manually. Each shortcut declares trigger
/// phrases that bind the localized "${applicationName}" placeholder so
/// "Hey Siri, ask Hygur ..." works out of the box.
struct HygurAppShortcuts: AppShortcutsProvider {
    static var appShortcuts: [AppShortcut] {
        AppShortcut(
            intent: AskHygurIntent(),
            phrases: [
                "Ask \(.applicationName)",
                "Ask \(.applicationName) a question",
                "Search \(.applicationName)"
            ],
            shortTitle: "Ask Hygur",
            systemImageName: "questionmark.bubble"
        )

        AppShortcut(
            intent: SaveToHygurIntent(),
            phrases: [
                "Save to \(.applicationName)",
                "Save this in \(.applicationName)"
            ],
            shortTitle: "Save to Hygur",
            systemImageName: "tray.and.arrow.down"
        )

        AppShortcut(
            intent: WhatsOnMyAgendaIntent(),
            phrases: [
                "What's on my agenda in \(.applicationName)",
                "Show my \(.applicationName) agenda",
                "What does \(.applicationName) have for today"
            ],
            shortTitle: "What's on my agenda",
            systemImageName: "calendar"
        )

        AppShortcut(
            intent: CreateNoteInHygurIntent(),
            phrases: [
                "Create a note in \(.applicationName)",
                "New \(.applicationName) note"
            ],
            shortTitle: "Create note in Hygur",
            systemImageName: "note.text.badge.plus"
        )
    }
}
