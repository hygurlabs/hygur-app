import SwiftUI

// MARK: - Tags View

/// Main view for displaying and managing tags with colors.
struct TagsView: View {
    @StateObject private var viewModel = TagsViewModel()
    @State private var showingNewTag = false
    @State private var searchText = ""
    @State private var errorMessage: String?

    var body: some View {
        VStack(spacing: 0) {
            // Header
            FeatureHeader(title: "Tags", count: viewModel.tags.count) {
                IconButton(systemImage: "plus", label: "New Tag") {
                    showingNewTag = true
                }
            }

            Divider()

            // Content
            if viewModel.isLoading {
                LoadingIndicator(style: .large)
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
            } else if viewModel.filteredTags(searchText: searchText).isEmpty {
                emptyState
            } else {
                tagList
            }
        }
        .toolbar {
            ToolbarItem(placement: .navigation) {
                ToolbarSearchField(text: $searchText, prompt: "Search tags")
            }
        }
        .sheet(isPresented: $showingNewTag) {
            NewTagSheet(viewModel: viewModel)
        }
        .task {
            await viewModel.loadTags()
        }
        .onChange(of: viewModel.showError) { _, isShowing in
            if isShowing {
                errorMessage = viewModel.errorMessage
                viewModel.showError = false
            }
        }
        .errorBannerOverlay($errorMessage)
    }

    // MARK: - Empty State

    private var emptyState: some View {
        Group {
            if searchText.isEmpty {
                EmptyStateView(
                    icon: "tag",
                    title: "No tags yet",
                    subtitle: "Create tags to organize your notes and knowledge items",
                    action: ("Create Tag", { showingNewTag = true })
                )
            } else {
                EmptyStateView(
                    icon: "magnifyingglass",
                    title: "No tags matching \"\(searchText)\""
                )
            }
        }
    }

    // MARK: - Tag List

    private var tagList: some View {
        List {
            ForEach(viewModel.filteredTags(searchText: searchText)) { tag in
                TagRow(tag: tag, viewModel: viewModel)
            }
        }
        .listStyle(.inset)
    }
}

// MARK: - Tag Row

/// Single tag row in the list.
struct TagRow: View {
    let tag: Tag
    @ObservedObject var viewModel: TagsViewModel
    @State private var showingEditSheet = false
    @State private var showingDeleteConfirmation = false
    @State private var showingDetailSheet = false

    var body: some View {
        HStack(spacing: HygurSpacing.md) {
            // Color indicator
            Circle()
                .fill(tag.swiftUIColor)
                .frame(width: 12, height: 12)

            // Tag name and pill preview
            VStack(alignment: .leading, spacing: HygurSpacing.xs) {
                Text(tag.name)
                    .font(HygurTypography.headline)

                HStack(spacing: HygurSpacing.sm) {
                    TagPillView(tag: tag)

                    Text("\(tag.usageCount) items")
                        .font(HygurTypography.caption)
                        .foregroundStyle(HygurColors.textTertiary)
                }
            }

            Spacer()

            // Usage count badge
            Text("\(tag.usageCount)")
                .font(HygurTypography.caption)
                .fontWeight(.medium)
                .padding(.horizontal, HygurSpacing.sm)
                .padding(.vertical, HygurSpacing.xs)
                .background(Color.secondary.opacity(0.15))
                .clipShape(Capsule())
        }
        .padding(.vertical, HygurSpacing.xs)
        .contentShape(Rectangle())
        .onTapGesture(count: 2) {
            showingDetailSheet = true
        }
        .contextMenu {
            contextMenuItems
        }
        .sheet(isPresented: $showingEditSheet) {
            EditTagSheet(tag: tag, viewModel: viewModel)
        }
        .sheet(isPresented: $showingDetailSheet) {
            TagDetailView(tag: tag)
        }
        .confirmationDialog(
            "Delete Tag",
            isPresented: $showingDeleteConfirmation,
            titleVisibility: .visible
        ) {
            Button("Delete", role: .destructive) {
                Task {
                    await viewModel.deleteTag(tag)
                }
            }
            Button("Cancel", role: .cancel) {}
        } message: {
            Text("Are you sure you want to delete \"\(tag.name)\"? This will remove the tag from all items.")
        }
    }

    // MARK: - Context Menu

    @ViewBuilder
    private var contextMenuItems: some View {
        Button {
            showingDetailSheet = true
        } label: {
            Label("View Items", systemImage: "doc.text.magnifyingglass")
        }

        Button {
            showingEditSheet = true
        } label: {
            Label("Edit", systemImage: "pencil")
        }

        Divider()

        Button(role: .destructive) {
            showingDeleteConfirmation = true
        } label: {
            Label("Delete", systemImage: "trash")
        }
    }
}

// MARK: - New Tag Sheet

/// Sheet for creating a new tag.
struct NewTagSheet: View {
    @ObservedObject var viewModel: TagsViewModel
    @Environment(\.dismiss) private var dismiss
    @State private var name = ""
    @State private var selectedColor: TagColor = .blue
    @State private var isCreating = false

    var body: some View {
        VStack(spacing: HygurSpacing.xl) {
            Text("New Tag")
                .font(HygurTypography.headline)

            // Name field
            VStack(alignment: .leading, spacing: HygurSpacing.sm) {
                Text("Name")
                    .font(HygurTypography.subheadline)
                    .foregroundStyle(HygurColors.textSecondary)
                TextField("Tag Name", text: $name)
                    .textFieldStyle(.roundedBorder)
            }

            // Color picker
            VStack(alignment: .leading, spacing: HygurSpacing.sm) {
                Text("Color")
                    .font(HygurTypography.subheadline)
                    .foregroundStyle(HygurColors.textSecondary)

                colorGrid
            }

            // Preview
            if !name.isEmpty {
                VStack(alignment: .leading, spacing: HygurSpacing.sm) {
                    Text("Preview")
                        .font(HygurTypography.subheadline)
                        .foregroundStyle(HygurColors.textSecondary)
                    SimpleTagPillView(name: name, color: selectedColor.color)
                }
            }

            // Actions
            HStack {
                Button("Cancel", role: .cancel) { dismiss() }
                    .keyboardShortcut(.escape, modifiers: [])

                Spacer()

                Button("Create") {
                    createTag()
                }
                .buttonStyle(.borderedProminent)
                .disabled(name.isEmpty || isCreating)
                .keyboardShortcut(.return, modifiers: .command)
            }
        }
        .padding(HygurSpacing.lg)
        .frame(width: 350)
    }

    private var colorGrid: some View {
        LazyVGrid(columns: Array(repeating: GridItem(.fixed(32), spacing: HygurSpacing.sm), count: 8), spacing: HygurSpacing.sm) {
            ForEach(TagColor.allCases) { color in
                TagColorSwatchView(tagColor: color, isSelected: selectedColor == color) {
                    selectedColor = color
                }
            }
        }
    }

    private func createTag() {
        guard !name.isEmpty else { return }

        isCreating = true
        Task {
            await viewModel.createTag(name: name, color: selectedColor.rawValue)
            dismiss()
        }
    }
}

// MARK: - Edit Tag Sheet

/// Sheet for editing an existing tag.
struct EditTagSheet: View {
    let tag: Tag
    @ObservedObject var viewModel: TagsViewModel
    @Environment(\.dismiss) private var dismiss
    @State private var name: String
    @State private var selectedColor: TagColor
    @State private var isSaving = false

    init(tag: Tag, viewModel: TagsViewModel) {
        self.tag = tag
        self.viewModel = viewModel
        _name = State(initialValue: tag.name)
        // Find matching TagColor or default to blue
        _selectedColor = State(initialValue: TagColor.allCases.first { $0.rawValue.uppercased() == tag.color.uppercased() } ?? .blue)
    }

    var body: some View {
        VStack(spacing: HygurSpacing.xl) {
            Text("Edit Tag")
                .font(HygurTypography.headline)

            // Name field
            VStack(alignment: .leading, spacing: HygurSpacing.sm) {
                Text("Name")
                    .font(HygurTypography.subheadline)
                    .foregroundStyle(HygurColors.textSecondary)
                TextField("Tag Name", text: $name)
                    .textFieldStyle(.roundedBorder)
            }

            // Color picker
            VStack(alignment: .leading, spacing: HygurSpacing.sm) {
                Text("Color")
                    .font(HygurTypography.subheadline)
                    .foregroundStyle(HygurColors.textSecondary)

                colorGrid
            }

            // Preview
            VStack(alignment: .leading, spacing: HygurSpacing.sm) {
                Text("Preview")
                    .font(HygurTypography.subheadline)
                    .foregroundStyle(HygurColors.textSecondary)
                SimpleTagPillView(name: name.isEmpty ? tag.name : name, color: selectedColor.color)
            }

            // Actions
            HStack {
                Button("Cancel", role: .cancel) { dismiss() }
                    .keyboardShortcut(.escape, modifiers: [])

                Spacer()

                Button("Save") {
                    saveTag()
                }
                .buttonStyle(.borderedProminent)
                .disabled(name.isEmpty || isSaving)
                .keyboardShortcut(.return, modifiers: .command)
            }
        }
        .padding(HygurSpacing.lg)
        .frame(width: 350)
    }

    private var colorGrid: some View {
        LazyVGrid(columns: Array(repeating: GridItem(.fixed(32), spacing: HygurSpacing.sm), count: 8), spacing: HygurSpacing.sm) {
            ForEach(TagColor.allCases) { color in
                TagColorSwatchView(tagColor: color, isSelected: selectedColor == color) {
                    selectedColor = color
                }
            }
        }
    }

    private func saveTag() {
        guard !name.isEmpty else { return }

        isSaving = true
        Task {
            await viewModel.updateTag(tag, name: name, color: selectedColor.rawValue)
            dismiss()
        }
    }
}

// MARK: - Tag Detail View

/// Sheet showing all documents with a tag
struct TagDetailView: View {
    let tag: Tag
    @Environment(\.dismiss) private var dismiss
    @State private var items: [TagItem] = []
    @State private var isLoading = false
    @State private var errorMessage: String?

    private let sidecar = SidecarService.fromSettings()

    var body: some View {
        VStack(spacing: 0) {
            header

            Divider()

            if isLoading {
                LoadingIndicator(style: .large)
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
            } else if let error = errorMessage, items.isEmpty {
                errorState(error)
            } else if items.isEmpty {
                EmptyStateView(
                    icon: "doc.text",
                    title: "No items",
                    subtitle: "No documents have this tag"
                )
            } else {
                itemList
            }
        }
        .frame(minWidth: 500, minHeight: 450)
        .task {
            await loadItems()
        }
        .errorBannerOverlay($errorMessage)
    }

    // MARK: - Header

    private var header: some View {
        HStack(spacing: HygurSpacing.md) {
            Circle()
                .fill(tag.swiftUIColor)
                .frame(width: 16, height: 16)

            VStack(alignment: .leading, spacing: HygurSpacing.xxs) {
                Text(tag.name)
                    .font(HygurTypography.headline)
                Text("\(items.count) items")
                    .font(HygurTypography.caption)
                    .foregroundStyle(HygurColors.textSecondary)
            }
            Spacer()
            Button("Done") {
                dismiss()
            }
            .keyboardShortcut(.escape)
        }
        .padding(HygurSpacing.lg)
    }

    // MARK: - Error State

    private func errorState(_ message: String) -> some View {
        VStack(spacing: HygurSpacing.md) {
            Image(systemName: "exclamationmark.triangle")
                .font(.system(size: 40))
                .foregroundStyle(HygurColors.warning)
            Text("Failed to load items")
                .font(HygurTypography.headline)
            Text(message)
                .font(HygurTypography.caption)
                .foregroundStyle(HygurColors.textSecondary)
            Button("Retry") {
                Task { await loadItems() }
            }
            .buttonStyle(.bordered)
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
    }

    // MARK: - Item List

    private var itemList: some View {
        List {
            ForEach(items) { item in
                TagItemRow(item: item, tag: tag, onRemove: {
                    await removeItem(item)
                })
            }
            .onDelete { indexSet in
                Task {
                    for index in indexSet {
                        await removeItem(items[index])
                    }
                }
            }
        }
        .listStyle(.inset)
    }

    // MARK: - Actions

    private func loadItems() async {
        isLoading = true
        errorMessage = nil
        defer { isLoading = false }

        do {
            items = try await sidecar.listTagItems(tagId: tag.id)
        } catch {
            errorMessage = error.localizedDescription
            print("TagDetailView error: \(error)")
        }
    }

    private func removeItem(_ item: TagItem) async {
        do {
            _ = try await sidecar.removeTagFromItem(contentId: item.id, tagId: tag.id)
            items.removeAll { $0.id == item.id }
        } catch {
            errorMessage = error.localizedDescription
            print("Failed to remove tag from item: \(error)")
        }
    }
}

// MARK: - Tag Item Row

/// Single document row in the tag detail view with swipe-to-delete
struct TagItemRow: View {
    let item: TagItem
    let tag: Tag
    let onRemove: () async -> Void

    @State private var isRemoving = false

    var body: some View {
        HStack(spacing: HygurSpacing.md) {
            Image(systemName: item.sourceTypeIcon)
                .font(.title3)
                .foregroundColor(HygurColors.accent)
                .frame(width: 28)

            VStack(alignment: .leading, spacing: HygurSpacing.xxs) {
                Text(item.title)
                    .font(HygurTypography.body)
                    .lineLimit(1)

                HStack(spacing: HygurSpacing.sm) {
                    Text(item.sourceType)
                        .font(HygurTypography.caption)
                        .padding(.horizontal, HygurSpacing.xs)
                        .padding(.vertical, HygurSpacing.xxs)
                        .background(Color.secondary.opacity(0.15))
                        .cornerRadius(HygurRadius.xs)

                    if let path = item.sourcePath {
                        Text(path)
                            .font(HygurTypography.caption)
                            .foregroundStyle(HygurColors.textTertiary)
                            .lineLimit(1)
                    }

                    Spacer()

                    Text(formattedDate(item.updatedAt))
                        .font(HygurTypography.caption)
                        .foregroundStyle(HygurColors.textTertiary)
                }
            }

            Spacer()

            // Remove button
            Button {
                isRemoving = true
                Task {
                    await onRemove()
                    isRemoving = false
                }
            } label: {
                Image(systemName: "xmark.circle.fill")
                    .foregroundStyle(HygurColors.textSecondary)
            }
            .buttonStyle(.plain)
            .opacity(isRemoving ? 0.5 : 1)
            .disabled(isRemoving)
            .help("Remove tag from this item")
        }
        .padding(.vertical, HygurSpacing.xs)
        .swipeActions(edge: .trailing, allowsFullSwipe: true) {
            Button(role: .destructive) {
                Task { await onRemove() }
            } label: {
                Label("Remove", systemImage: "trash")
            }
        }
    }

    private func formattedDate(_ date: Date) -> String {
        let formatter = RelativeDateTimeFormatter()
        formatter.unitsStyle = .abbreviated
        return formatter.localizedString(for: date, relativeTo: Date())
    }
}

// MARK: - View Model

/// View model for the tags list.
@MainActor
class TagsViewModel: ObservableObject {
    @Published var tags: [Tag] = []
    @Published var isLoading = false
    @Published var showError = false
    @Published var errorMessage = ""

    private let sidecar = SidecarService.fromSettings()

    // MARK: - Filtering

    func filteredTags(searchText: String) -> [Tag] {
        if searchText.isEmpty {
            return tags.sorted { $0.usageCount > $1.usageCount }
        }
        return tags
            .filter { $0.name.localizedCaseInsensitiveContains(searchText) }
            .sorted { $0.usageCount > $1.usageCount }
    }

    // MARK: - Actions

    func loadTags() async {
        isLoading = true
        defer { isLoading = false }

        do {
            tags = try await sidecar.listTags()
        } catch {
            showError(error)
        }
    }

    func createTag(name: String, color: String) async {
        do {
            let tag = try await sidecar.createTag(name: name, color: color)
            tags.insert(tag, at: 0)
        } catch {
            showError(error)
        }
    }

    func updateTag(_ tag: Tag, name: String, color: String) async {
        do {
            let updated = try await sidecar.updateTag(id: tag.id, name: name, color: color)
            if let index = tags.firstIndex(where: { $0.id == tag.id }) {
                tags[index] = updated
            }
        } catch {
            showError(error)
        }
    }

    func deleteTag(_ tag: Tag) async {
        do {
            try await sidecar.deleteTag(id: tag.id)
            tags.removeAll { $0.id == tag.id }
        } catch {
            showError(error)
        }
    }

    // MARK: - Error Handling

    private func showError(_ error: Error) {
        errorMessage = error.localizedDescription
        showError = true
        print("TagsViewModel error: \(error)")
    }
}

// MARK: - Previews

#Preview("Tags View") {
    TagsView()
        .frame(width: 400, height: 500)
}
