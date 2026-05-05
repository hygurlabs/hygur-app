import Foundation
import SwiftUI

@MainActor
@Observable
final class DocumentQuickLookViewModel {
    // MARK: - Item state

    var item: KnowledgeItemResponse?
    var isLoading = false
    var error: String?

    // MARK: - Tag state

    var currentTags: [Tag] = []
    var availableTags: [Tag] = []
    var isLoadingTags = false
    var isSavingTag = false

    // MARK: - Project state

    var projects: [Project] = []
    var selectedProjectId: String?
    var isLoadingProjects = false
    var isSavingProject = false
    var projectChanged: Bool { selectedProjectId != originalProjectId }
    private var originalProjectId: String?

    // MARK: - Note state

    var noteTitle: String = ""
    var noteContent: String = ""
    var isSavingNote = false
    var noteSaved = false

    // MARK: - Computed

    var fullText: String { item?.normalizedText ?? "" }

    var unassignedTags: [Tag] {
        availableTags.filter { tag in !currentTags.contains { $0.id == tag.id } }
    }

    // MARK: - Private

    private let sidecar = SidecarService.fromSettings()
    private var contentId: String = ""

    // MARK: - Load

    func load(contentId: String) async {
        self.contentId = contentId
        isLoading = true
        error = nil
        defer { isLoading = false }

        await withTaskGroup(of: Void.self) { group in
            group.addTask { await self.fetchItem(contentId: contentId) }
            group.addTask { await self.loadTags() }
            group.addTask { await self.loadProjects() }
        }
    }

    private func fetchItem(contentId: String) async {
        do {
            if let fetched = try await sidecar.getKnowledgeItemFull(contentId: contentId) {
                item = fetched
                selectedProjectId = fetched.projectId
                originalProjectId = fetched.projectId
                currentTags = fetched.tags.map { Tag(id: $0.id, name: $0.name, color: $0.color, usageCount: 0) }
            }
        } catch {
            self.error = error.localizedDescription
        }
    }

    private func loadTags() async {
        isLoadingTags = true
        defer { isLoadingTags = false }
        do {
            availableTags = try await sidecar.listTags()
        } catch {
            print("[QuickLook] Failed to load tags: \(error)")
        }
    }

    private func loadProjects() async {
        isLoadingProjects = true
        defer { isLoadingProjects = false }
        do {
            projects = try await sidecar.listProjects().filter { !$0.archived }
        } catch {
            print("[QuickLook] Failed to load projects: \(error)")
        }
    }

    // MARK: - Tags

    func addTag(_ tag: Tag) async {
        isSavingTag = true
        defer { isSavingTag = false }
        do {
            let updated = try await sidecar.addTagToItem(contentId: contentId, tagId: tag.id)
            currentTags = updated.tags
        } catch {
            print("[QuickLook] addTag failed: \(error)")
        }
    }

    func removeTag(_ tag: Tag) async {
        isSavingTag = true
        defer { isSavingTag = false }
        do {
            let updated = try await sidecar.removeTagFromItem(contentId: contentId, tagId: tag.id)
            currentTags = updated.tags
        } catch {
            print("[QuickLook] removeTag failed: \(error)")
        }
    }

    // MARK: - Project

    func saveProject() async {
        guard projectChanged else { return }
        isSavingProject = true
        defer { isSavingProject = false }
        do {
            if let pid = selectedProjectId {
                let updated = try await sidecar.linkItemToProject(contentId: contentId, projectId: pid)
                currentTags = updated.tags
            } else {
                _ = try await sidecar.unlinkItemFromProject(contentId: contentId)
            }
            originalProjectId = selectedProjectId
        } catch {
            selectedProjectId = originalProjectId
            print("[QuickLook] saveProject failed: \(error)")
        }
    }

    func revertProject() {
        selectedProjectId = originalProjectId
    }

    // MARK: - Note

    func createLinkedNote() async {
        guard !noteTitle.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty,
              !noteContent.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty else { return }

        isSavingNote = true
        defer { isSavingNote = false }

        let sourceTitle = item?.title ?? contentId
        let body = "> Lié à : \(sourceTitle) (\(contentId))\n\n\(noteContent)"

        do {
            _ = try await sidecar.createNote(
                title: noteTitle,
                content: body,
                projectId: selectedProjectId,
                tagIds: currentTags.isEmpty ? nil : currentTags.map(\.id)
            )
            noteSaved = true
            noteTitle = ""
            noteContent = ""
            // Reset the confirmation badge after 2s
            Task {
                try? await Task.sleep(nanoseconds: 2_000_000_000)
                noteSaved = false
            }
        } catch {
            print("[QuickLook] createNote failed: \(error)")
        }
    }
}
