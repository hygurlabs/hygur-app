import Foundation
import CoreSpotlight
import UniformTypeIdentifiers

/// Indexes chat sessions and notes into Core Spotlight so the user can
/// find them from the Spotlight menu (or Mail/Finder search bars).
///
/// Each item carries a `hygur://session/<uuid>` or `hygur://note/<id>`
/// content URL; clicking the result re-launches the app and `HygurApp`'s
/// `.onOpenURL` handler routes the navigation.
///
/// Errors from CSSearchableIndex are intentionally swallowed and logged —
/// search indexing is best-effort and must never block the UI.
@MainActor
enum SpotlightIndexer {
    private static let sessionDomain = "com.hygur.app.session"
    private static let noteDomain = "com.hygur.app.note"

    // MARK: - Sessions

    static func index(session: ChatSession) {
        guard session.hasMessages else {
            // Empty sessions clutter Spotlight with "New Chat" placeholders.
            removeSession(id: session.id)
            return
        }

        let attrs = CSSearchableItemAttributeSet(contentType: .text)
        attrs.title = session.displayTitle
        attrs.contentDescription = session.lastMessagePreview ?? ""
        attrs.contentModificationDate = session.updatedAt
        attrs.contentCreationDate = session.createdAt
        attrs.identifier = session.id.uuidString
        attrs.relatedUniqueIdentifier = session.id.uuidString
        // Surface the full message content so Spotlight can match on body
        // text, not just titles.
        attrs.textContent = session.messages.map(\.content).joined(separator: "\n\n")

        attrs.contentURL = URL(string: "hygur://session/\(session.id.uuidString)")
        let item = CSSearchableItem(
            uniqueIdentifier: sessionItemID(session.id),
            domainIdentifier: sessionDomain,
            attributeSet: attrs
        )
        item.expirationDate = .distantFuture

        CSSearchableIndex.default().indexSearchableItems([item]) { error in
            if let error { Self.logFailure("indexSession", error) }
        }
    }

    static func removeSession(id: UUID) {
        CSSearchableIndex.default().deleteSearchableItems(withIdentifiers: [sessionItemID(id)]) { error in
            if let error { Self.logFailure("removeSession", error) }
        }
    }

    static func reindexAllSessions(_ sessions: [ChatSession]) {
        let items: [CSSearchableItem] = sessions.compactMap { session -> CSSearchableItem? in
            guard session.hasMessages else { return nil }
            let attrs = CSSearchableItemAttributeSet(contentType: .text)
            attrs.title = session.displayTitle
            attrs.contentDescription = session.lastMessagePreview ?? ""
            attrs.contentModificationDate = session.updatedAt
            attrs.contentCreationDate = session.createdAt
            attrs.textContent = session.messages.map(\.content).joined(separator: "\n\n")
            attrs.contentURL = URL(string: "hygur://session/\(session.id.uuidString)")
            return CSSearchableItem(
                uniqueIdentifier: sessionItemID(session.id),
                domainIdentifier: sessionDomain,
                attributeSet: attrs
            )
        }
        guard !items.isEmpty else { return }
        CSSearchableIndex.default().indexSearchableItems(items) { error in
            if let error { Self.logFailure("reindexAllSessions", error) }
        }
    }

    // MARK: - Notes

    static func index(note: Note) {
        let attrs = CSSearchableItemAttributeSet(contentType: .text)
        attrs.title = note.title
        attrs.contentDescription = String(note.content.prefix(160))
        attrs.contentCreationDate = note.createdAt
        attrs.contentModificationDate = note.updatedAt
        attrs.identifier = note.id
        attrs.relatedUniqueIdentifier = note.id
        attrs.textContent = note.content
        attrs.keywords = note.tags.map(\.name)
        attrs.contentURL = URL(string: "hygur://note/\(note.id)")

        let item = CSSearchableItem(
            uniqueIdentifier: noteItemID(note.id),
            domainIdentifier: noteDomain,
            attributeSet: attrs
        )
        item.expirationDate = .distantFuture

        CSSearchableIndex.default().indexSearchableItems([item]) { error in
            if let error { Self.logFailure("indexNote", error) }
        }
    }

    static func removeNote(id: String) {
        CSSearchableIndex.default().deleteSearchableItems(withIdentifiers: [noteItemID(id)]) { error in
            if let error { Self.logFailure("removeNote", error) }
        }
    }

    static func reindexAllNotes(_ notes: [Note]) {
        let items: [CSSearchableItem] = notes.map { note in
            let attrs = CSSearchableItemAttributeSet(contentType: .text)
            attrs.title = note.title
            attrs.contentDescription = String(note.content.prefix(160))
            attrs.contentCreationDate = note.createdAt
            attrs.contentModificationDate = note.updatedAt
            attrs.textContent = note.content
            attrs.keywords = note.tags.map(\.name)
            attrs.contentURL = URL(string: "hygur://note/\(note.id)")
            return CSSearchableItem(
                uniqueIdentifier: noteItemID(note.id),
                domainIdentifier: noteDomain,
                attributeSet: attrs
            )
        }
        guard !items.isEmpty else { return }
        CSSearchableIndex.default().indexSearchableItems(items) { error in
            if let error { Self.logFailure("reindexAllNotes", error) }
        }
    }

    // MARK: - Bulk wipe (used by reset / restore flows)

    static func clearAll() {
        CSSearchableIndex.default().deleteSearchableItems(withDomainIdentifiers: [sessionDomain, noteDomain]) { error in
            if let error { Self.logFailure("clearAll", error) }
        }
    }

    static func clearNotes() {
        CSSearchableIndex.default().deleteSearchableItems(withDomainIdentifiers: [noteDomain]) { error in
            if let error { Self.logFailure("clearNotes", error) }
        }
    }

    // MARK: - Identifiers

    private static func sessionItemID(_ id: UUID) -> String {
        "session:\(id.uuidString)"
    }

    private static func noteItemID(_ id: String) -> String {
        "note:\(id)"
    }

    nonisolated private static func logFailure(_ op: String, _ error: Error) {
        // Best-effort; don't surface to the user. Called from CSSearchableIndex
        // completion handlers, which are nonisolated.
        NSLog("SpotlightIndexer.%@ failed: %@", op, error.localizedDescription)
    }
}
