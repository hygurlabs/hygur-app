import Foundation
import SwiftUI

/// Drives the command palette: builds the static command list, layers recent
/// chat sessions on top, runs the user's query through `FuzzyMatcher`, and
/// keeps a `selectedIndex` so arrow-key navigation in the sheet stays in sync.
@MainActor
@Observable
final class CommandPaletteViewModel {
    var query: String = "" {
        didSet { selectedIndex = 0 }
    }
    private(set) var selectedIndex: Int = 0

    private let sessionManager: ChatSessionManager?

    /// Cap on how many recent sessions we surface — palette is meant to feel
    /// instant, not be a full session browser.
    private let maxRecentSessions = 8

    init(sessionManager: ChatSessionManager? = nil) {
        self.sessionManager = sessionManager
    }

    /// All commands ordered as if the query were empty: built-in actions first
    /// (most-used at the top), then recent chat sessions.
    private var allCommands: [PaletteCommand] {
        var cmds: [PaletteCommand] = [
            PaletteCommand(
                id: "new-chat",
                title: "Nouveau chat",
                subtitle: "Démarrer une nouvelle conversation",
                icon: "plus.bubble",
                keywords: ["new", "chat", "conversation"],
                action: .createNewChat
            ),
            PaletteCommand(
                id: "new-note",
                title: "Nouvelle note",
                subtitle: "Créer une note dans la base",
                icon: "square.and.pencil",
                keywords: ["new", "note", "create"],
                action: .createNote
            ),
            PaletteCommand(
                id: "nav-chat",
                title: "Chat",
                subtitle: "Aller à la zone de conversation",
                icon: "bubble.left.and.bubble.right",
                keywords: ["chat", "discussion"],
                action: .navigate(.newChat)
            ),
            PaletteCommand(
                id: "nav-knowledge",
                title: "Knowledge Base",
                subtitle: "Parcourir les documents indexés",
                icon: "books.vertical",
                keywords: ["kb", "knowledge", "base", "documents"],
                action: .navigate(.knowledgeBase)
            ),
            PaletteCommand(
                id: "nav-search",
                title: "Recherche",
                subtitle: "Recherche sémantique brute",
                icon: "magnifyingglass",
                keywords: ["search", "query"],
                action: .navigate(.search)
            ),
            PaletteCommand(
                id: "nav-notes",
                title: "Notes",
                subtitle: "Liste des notes",
                icon: "note.text",
                keywords: ["notes"],
                action: .navigate(.notes)
            ),
            PaletteCommand(
                id: "nav-projects",
                title: "Projets",
                subtitle: "Gérer les projets",
                icon: "folder",
                keywords: ["projects", "project"],
                action: .navigate(.projects)
            ),
            PaletteCommand(
                id: "nav-tags",
                title: "Tags",
                subtitle: "Gérer les tags",
                icon: "tag",
                keywords: ["tags"],
                action: .navigate(.tags)
            ),
            PaletteCommand(
                id: "nav-email",
                title: "Email",
                subtitle: "Boîte mail indexée",
                icon: "envelope",
                keywords: ["mail", "email", "gmail", "proton"],
                action: .navigate(.email)
            ),
            PaletteCommand(
                id: "nav-timeline",
                title: "Timeline",
                subtitle: "Frise chronologique des événements",
                icon: "clock.arrow.circlepath",
                keywords: ["timeline", "frise", "chronologie", "graph", "history", "histoire"],
                action: .navigate(.graph)
            ),
            PaletteCommand(
                id: "nav-connectors",
                title: "Connecteurs",
                subtitle: "État des connecteurs",
                icon: "personalhotspot",
                keywords: ["connectors", "connecteurs", "sync"],
                action: .navigate(.connectors)
            ),
            PaletteCommand(
                id: "nav-activity",
                title: "Activité",
                subtitle: "Flux d'événements en temps réel",
                icon: "waveform.path.ecg",
                keywords: ["activity", "events", "logs"],
                action: .navigate(.activity)
            ),
            PaletteCommand(
                id: "sync-mail",
                title: "Synchroniser tous les comptes mail",
                subtitle: "Déclenche un sync immédiat",
                icon: "arrow.triangle.2.circlepath",
                keywords: ["sync", "mail", "email", "gmail", "proton", "fetch"],
                action: .syncAllMail
            ),
            PaletteCommand(
                id: "ingest-file",
                title: "Indexer un fichier…",
                subtitle: "Ajoute un document à la base",
                icon: "tray.and.arrow.down",
                keywords: ["ingest", "import", "file", "add", "upload"],
                action: .indexFile
            )
        ]

        if let sessionManager {
            // Recent sessions surface only if they have content — opening an
            // empty placeholder by accident is annoying.
            let recents = sessionManager.sessions
                .filter { $0.hasMessages }
                .prefix(maxRecentSessions)
            for session in recents {
                cmds.append(
                    PaletteCommand(
                        id: "session-\(session.id.uuidString)",
                        title: session.displayTitle,
                        subtitle: session.lastMessagePreview,
                        icon: "clock.arrow.circlepath",
                        keywords: ["session", "chat", "history", "récent"],
                        action: .openChatSession(session.id)
                    )
                )
            }
        }

        return cmds
    }

    /// Filtered + ranked commands for the current query.
    var filteredCommands: [PaletteCommand] {
        FuzzyMatcher.rank(items: allCommands, query: query) { $0.searchKey }
    }

    var selectedCommand: PaletteCommand? {
        let cmds = filteredCommands
        guard !cmds.isEmpty else { return nil }
        let idx = max(0, min(selectedIndex, cmds.count - 1))
        return cmds[idx]
    }

    func moveSelection(by delta: Int) {
        let cmds = filteredCommands
        guard !cmds.isEmpty else {
            selectedIndex = 0
            return
        }
        let next = (selectedIndex + delta) % cmds.count
        selectedIndex = next < 0 ? next + cmds.count : next
    }

    func setSelection(to index: Int) {
        selectedIndex = max(0, index)
    }

    func reset() {
        query = ""
        selectedIndex = 0
    }
}
