import SwiftUI

// MARK: - Knowledge Item Detail Sheet

/// Sheet for viewing and editing knowledge item tags and project association.
struct KnowledgeItemDetailSheet: View {
    @Environment(\.dismiss) private var dismiss
    @StateObject private var viewModel: KnowledgeItemDetailViewModel
    var onItemUpdated: ((KnowledgeItemResponse) -> Void)?

    init(item: KnowledgeItemResponse, onItemUpdated: ((KnowledgeItemResponse) -> Void)? = nil) {
        _viewModel = StateObject(wrappedValue: KnowledgeItemDetailViewModel(item: item))
        self.onItemUpdated = onItemUpdated
    }

    init(item: KnowledgeItem, onItemUpdated: ((KnowledgeItemResponse) -> Void)? = nil) {
        _viewModel = StateObject(wrappedValue: KnowledgeItemDetailViewModel(item: item))
        self.onItemUpdated = onItemUpdated
    }

    var body: some View {
        VStack(spacing: 0) {
            // Header
            header

            Divider()

            // Content
            ScrollView {
                VStack(alignment: .leading, spacing: HygurSpacing.xxl) {
                    // Item details section
                    itemDetailsSection

                    Divider()

                    // Project picker
                    projectPicker

                    Divider()

                    // Tags picker
                    tagsPicker
                }
                .padding(HygurSpacing.lg)
            }

            Divider()

            // Action bar
            actionBar
        }
        .frame(minWidth: 500, idealWidth: 550, minHeight: 450, idealHeight: 500)
        .task {
            await viewModel.loadData()
        }
        .alert("Error", isPresented: $viewModel.showError) {
            Button("OK") {
                viewModel.showError = false
            }
        } message: {
            Text(viewModel.errorMessage)
        }
    }

    // MARK: - Header

    private var header: some View {
        HStack {
            VStack(alignment: .leading, spacing: HygurSpacing.xs) {
                Text("Edit Knowledge Item")
                    .font(HygurTypography.headline)
                Text(viewModel.item.title)
                    .font(HygurTypography.subheadline)
                    .foregroundStyle(HygurColors.textSecondary)
                    .lineLimit(1)
            }
            Spacer()
            Button {
                dismiss()
            } label: {
                Image(systemName: "xmark.circle.fill")
                    .foregroundStyle(HygurColors.textSecondary)
            }
            .buttonStyle(.plain)
        }
        .padding(HygurSpacing.lg)
    }

    // MARK: - Item Details Section

    private var itemDetailsSection: some View {
        VStack(alignment: .leading, spacing: HygurSpacing.md) {
            Text("Details")
                .font(HygurTypography.subheadline)
                .foregroundStyle(HygurColors.textSecondary)

            Grid(alignment: .leading, horizontalSpacing: HygurSpacing.lg, verticalSpacing: HygurSpacing.sm) {
                GridRow {
                    Text("Type")
                        .foregroundStyle(HygurColors.textSecondary)
                    Text(viewModel.item.sourceType.uppercased())
                        .fontWeight(.medium)
                }

                GridRow {
                    Text("Chunks")
                        .foregroundStyle(HygurColors.textSecondary)
                    Text("\(viewModel.item.chunkCount)")
                }

                GridRow {
                    Text("Created")
                        .foregroundStyle(HygurColors.textSecondary)
                    Text(viewModel.item.createdAt, style: .date)
                }

                GridRow {
                    Text("Updated")
                        .foregroundStyle(HygurColors.textSecondary)
                    Text(viewModel.item.updatedAt, style: .date)
                }

                if let path = viewModel.item.sourcePath {
                    GridRow {
                        Text("Source")
                            .foregroundStyle(HygurColors.textSecondary)
                        Text(path)
                            .lineLimit(1)
                            .truncationMode(.middle)
                            .help(path)
                    }
                }
            }
            .font(HygurTypography.callout)
        }
    }

    // MARK: - Project Picker

    private var projectPicker: some View {
        VStack(alignment: .leading, spacing: HygurSpacing.sm) {
            Text("Project")
                .font(HygurTypography.subheadline)
                .foregroundStyle(HygurColors.textSecondary)

            if viewModel.isLoadingProjects {
                HStack(spacing: HygurSpacing.xs) {
                    LoadingIndicator(style: .small)
                    Text("Loading projects...")
                        .font(HygurTypography.caption)
                        .foregroundStyle(HygurColors.textSecondary)
                }
            } else {
                HStack {
                    Picker("Project", selection: $viewModel.selectedProjectId) {
                        Text("None")
                            .tag(String?.none)
                        ForEach(viewModel.projects) { project in
                            Text(project.name)
                                .tag(String?.some(project.id))
                        }
                    }
                    .labelsHidden()
                    .pickerStyle(.menu)
                    .disabled(viewModel.isSaving)

                    if viewModel.projectChanged {
                        Button {
                            Task {
                                await viewModel.saveProjectChange()
                                notifyUpdate()
                            }
                        } label: {
                            if viewModel.isSaving {
                                LoadingIndicator(style: .small)
                            } else {
                                Image(systemName: "checkmark.circle.fill")
                                    .foregroundStyle(.green)
                            }
                        }
                        .buttonStyle(.plain)
                        .disabled(viewModel.isSaving)

                        Button {
                            viewModel.revertProjectChange()
                        } label: {
                            Image(systemName: "xmark.circle.fill")
                                .foregroundStyle(HygurColors.textSecondary)
                        }
                        .buttonStyle(.plain)
                        .disabled(viewModel.isSaving)
                    }
                }
            }
        }
    }

    // MARK: - Tags Picker

    private var tagsPicker: some View {
        VStack(alignment: .leading, spacing: HygurSpacing.sm) {
            HStack {
                Text("Tags")
                    .font(HygurTypography.subheadline)
                    .foregroundStyle(HygurColors.textSecondary)
                Spacer()
                if !viewModel.currentTags.isEmpty {
                    Button("Clear all") {
                        Task {
                            await viewModel.removeAllTags()
                            notifyUpdate()
                        }
                    }
                    .font(HygurTypography.caption)
                    .buttonStyle(.plain)
                    .foregroundStyle(HygurColors.textSecondary)
                    .disabled(viewModel.isSaving)
                }
            }

            if viewModel.isLoadingTags {
                HStack(spacing: HygurSpacing.xs) {
                    LoadingIndicator(style: .small)
                    Text("Loading tags...")
                        .font(HygurTypography.caption)
                        .foregroundStyle(HygurColors.textSecondary)
                }
            } else if viewModel.availableTags.isEmpty {
                Text("No tags available. Create tags in the Tags section.")
                    .font(HygurTypography.caption)
                    .foregroundStyle(HygurColors.textTertiary)
            } else {
                // Current tags on item
                if !viewModel.currentTags.isEmpty {
                    VStack(alignment: .leading, spacing: HygurSpacing.xs) {
                        Text("Current Tags")
                            .font(HygurTypography.caption)
                            .foregroundStyle(HygurColors.textTertiary)
                        TagsFlowLayout(spacing: 8) {
                            ForEach(viewModel.currentTags) { tag in
                                TagPillView(tag: tag, showRemoveButton: true) {
                                    Task {
                                        await viewModel.removeTag(tag)
                                        notifyUpdate()
                                    }
                                }
                                .opacity(viewModel.isSaving ? 0.5 : 1)
                            }
                        }
                    }
                    .padding(.bottom, 8)
                }

                // Available tags to add
                if !viewModel.unassignedTags.isEmpty {
                    VStack(alignment: .leading, spacing: HygurSpacing.xs) {
                        Text("Add Tags")
                            .font(HygurTypography.caption)
                            .foregroundStyle(HygurColors.textTertiary)
                        TagsFlowLayout(spacing: 8) {
                            ForEach(viewModel.unassignedTags) { tag in
                                SelectableTagPillView(tag: tag, isSelected: false) {
                                    Task {
                                        await viewModel.addTag(tag)
                                        notifyUpdate()
                                    }
                                }
                                .disabled(viewModel.isSaving)
                            }
                        }
                    }
                }
            }
        }
    }

    // MARK: - Action Bar

    private var actionBar: some View {
        HStack {
            if let path = viewModel.item.sourcePath {
                Button {
                    NSWorkspace.shared.selectFile(path, inFileViewerRootedAtPath: "")
                } label: {
                    Label("Show in Finder", systemImage: "folder")
                }
            }

            Spacer()

            Button("Done") {
                dismiss()
            }
            .keyboardShortcut(.return)
        }
        .padding(HygurSpacing.lg)
    }

    // MARK: - Helpers

    private func notifyUpdate() {
        guard let callback = onItemUpdated else { return }
        let item = viewModel.item
        let formatter = ISO8601DateFormatter()
        formatter.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        let createdStr = formatter.string(from: item.createdAt)
        let updatedStr = formatter.string(from: item.updatedAt)
        let tagSummaries = item.tags.map { TagSummary(id: $0.id, name: $0.name, color: $0.color) }
        let updatedItem = KnowledgeItemResponse(
            contentId: item.contentId,
            sourceType: item.sourceType,
            sourcePath: item.sourcePath,
            title: item.title,
            normalizedText: nil,
            chunkCount: item.chunkCount,
            tags: tagSummaries,
            projectId: item.projectId,
            createdAt: createdStr,
            updatedAt: updatedStr,
            date: nil
        )
        callback(updatedItem)
    }
}

// MARK: - View Model

@MainActor
class KnowledgeItemDetailViewModel: ObservableObject {
    @Published var item: KnowledgeItem
    @Published var projects: [Project] = []
    @Published var availableTags: [Tag] = []

    @Published var selectedProjectId: String?
    @Published var currentTags: [Tag] = []

    @Published var isLoadingProjects = false
    @Published var isLoadingTags = false
    @Published var isSaving = false

    @Published var showError = false
    @Published var errorMessage = ""

    private let sidecar = SidecarService.fromSettings()
    private var originalProjectId: String?

    init(item: KnowledgeItem) {
        self.item = item
        self.selectedProjectId = item.projectId
        self.originalProjectId = item.projectId
        self.currentTags = item.tags
    }

    convenience init(item: KnowledgeItemResponse) {
        let formatter = ISO8601DateFormatter()
        formatter.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        let created = formatter.date(from: item.createdAt) ?? Date()
        let updated = formatter.date(from: item.updatedAt) ?? Date()
        let tagItems = item.tags.map { Tag(id: $0.id, name: $0.name, color: $0.color, usageCount: 0) }
        let legacyItem = KnowledgeItem(
            contentId: item.contentId,
            sourceType: item.sourceType,
            sourcePath: item.sourcePath,
            title: item.title,
            chunkCount: item.chunkCount,
            tags: tagItems,
            projectId: item.projectId,
            createdAt: created,
            updatedAt: updated
        )
        self.init(item: legacyItem)
    }

    var projectChanged: Bool {
        selectedProjectId != originalProjectId
    }

    var unassignedTags: [Tag] {
        availableTags.filter { tag in
            !currentTags.contains { $0.id == tag.id }
        }
    }

    func loadData() async {
        await withTaskGroup(of: Void.self) { group in
            group.addTask { await self.loadProjects() }
            group.addTask { await self.loadTags() }
        }
    }

    func loadProjects() async {
        isLoadingProjects = true
        defer { isLoadingProjects = false }

        do {
            projects = try await sidecar.listProjects()
                .filter { !$0.archived }
        } catch {
            print("Failed to load projects: \(error)")
        }
    }

    func loadTags() async {
        isLoadingTags = true
        defer { isLoadingTags = false }

        do {
            availableTags = try await sidecar.listTags()
        } catch {
            print("Failed to load tags: \(error)")
        }
    }

    func saveProjectChange() async {
        guard projectChanged else { return }

        isSaving = true
        defer { isSaving = false }

        do {
            if let newProjectId = selectedProjectId {
                item = try await sidecar.linkItemToProject(contentId: item.contentId, projectId: newProjectId)
            } else {
                item = try await sidecar.unlinkItemFromProject(contentId: item.contentId)
            }
            originalProjectId = selectedProjectId
            currentTags = item.tags
        } catch {
            showError(error)
            // Revert on error
            selectedProjectId = originalProjectId
        }
    }

    func revertProjectChange() {
        selectedProjectId = originalProjectId
    }

    func addTag(_ tag: Tag) async {
        isSaving = true
        defer { isSaving = false }

        do {
            item = try await sidecar.addTagToItem(contentId: item.contentId, tagId: tag.id)
            currentTags = item.tags
        } catch {
            showError(error)
        }
    }

    func removeTag(_ tag: Tag) async {
        isSaving = true
        defer { isSaving = false }

        do {
            item = try await sidecar.removeTagFromItem(contentId: item.contentId, tagId: tag.id)
            currentTags = item.tags
        } catch {
            showError(error)
        }
    }

    func removeAllTags() async {
        for tag in currentTags {
            await removeTag(tag)
        }
    }

    private func showError(_ error: Error) {
        errorMessage = error.localizedDescription
        showError = true
        print("KnowledgeItemDetailViewModel error: \(error)")
    }
}

// MARK: - Flow Layout

/// Simple flow layout for tags (local to this file to avoid collision)
private struct TagsFlowLayout: Layout {
    var spacing: CGFloat = 8

    func sizeThatFits(proposal: ProposedViewSize, subviews: Subviews, cache: inout ()) -> CGSize {
        let result = arrangeSubviews(proposal: proposal, subviews: subviews)
        return result.size
    }

    func placeSubviews(in bounds: CGRect, proposal: ProposedViewSize, subviews: Subviews, cache: inout ()) {
        let result = arrangeSubviews(proposal: proposal, subviews: subviews)
        for (index, position) in result.positions.enumerated() {
            subviews[index].place(
                at: CGPoint(x: bounds.minX + position.x, y: bounds.minY + position.y),
                proposal: .unspecified
            )
        }
    }

    private func arrangeSubviews(proposal: ProposedViewSize, subviews: Subviews) -> (size: CGSize, positions: [CGPoint]) {
        let maxWidth = proposal.width ?? .infinity
        var positions: [CGPoint] = []
        var currentX: CGFloat = 0
        var currentY: CGFloat = 0
        var lineHeight: CGFloat = 0

        for subview in subviews {
            let size = subview.sizeThatFits(.unspecified)

            if currentX + size.width > maxWidth && currentX > 0 {
                currentX = 0
                currentY += lineHeight + spacing
                lineHeight = 0
            }

            positions.append(CGPoint(x: currentX, y: currentY))
            currentX += size.width + spacing
            lineHeight = max(lineHeight, size.height)
        }

        let totalHeight = currentY + lineHeight
        return (CGSize(width: maxWidth, height: totalHeight), positions)
    }
}

// MARK: - Preview

#Preview("Knowledge Item Detail") {
    let sampleItem = KnowledgeItem(
        contentId: "test-123",
        sourceType: "markdown",
        sourcePath: "/Users/test/Documents/notes.md",
        title: "Sample Document Title",
        chunkCount: 5,
        tags: [
            Tag(id: "tag-1", name: "Important", color: "#E53935"),
            Tag(id: "tag-2", name: "Work", color: "#1E88E5")
        ],
        projectId: nil,
        createdAt: Date(),
        updatedAt: Date()
    )

    KnowledgeItemDetailSheet(item: sampleItem)
}
