import Foundation

/// Discrete actions a palette entry can dispatch back to ContentView.
/// The view layer never executes these directly — it bubbles them up via the
/// palette's `onExecute` callback so the app's source-of-truth state (sidebar
/// selection, sheet flags, session manager) stays in one place.
enum CommandAction: Equatable {
    case navigate(SidebarItem)
    case openChatSession(UUID)
    case createNewChat
    case createNote
    case syncAllMail
    case indexFile
}

/// One row in the palette. `searchKey` is what the fuzzy matcher scores
/// against — it concatenates title + subtitle + a few keywords so typing
/// "kb" or "knowledge" both surface the Knowledge Base entry.
struct PaletteCommand: Identifiable, Equatable {
    let id: String
    let title: String
    let subtitle: String?
    let icon: String
    let action: CommandAction
    let searchKey: String

    init(
        id: String,
        title: String,
        subtitle: String? = nil,
        icon: String,
        keywords: [String] = [],
        action: CommandAction
    ) {
        self.id = id
        self.title = title
        self.subtitle = subtitle
        self.icon = icon
        self.action = action
        var key = title
        if let subtitle { key += " " + subtitle }
        for kw in keywords { key += " " + kw }
        self.searchKey = key
    }
}
