import SwiftUI

/// Sheet for organizing a chat session into projects and tags.
struct ChatOrganizeSheet: View {
    let session: ChatSession
    var onUpdateProject: ((String?) -> Void)?
    var onUpdateTags: (([String]) -> Void)?

    @Environment(\.dismiss) private var dismiss
    @State private var projects: [Project] = []
    @State private var tags: [Tag] = []
    @State private var selectedProjectId: String?
    @State private var selectedTagIds: Set<String> = []
    @State private var isLoading = true
    @State private var errorMessage: String?

    private let sidecar = SidecarService.fromSettings()

    var body: some View {
        VStack(spacing: 0) {
            header

            Divider()

            if isLoading {
                loadingState
            } else if let error = errorMessage {
                errorState(error)
            } else {
                ScrollView {
                    VStack(alignment: .leading, spacing: 20) {
                        projectSection
                        tagsSection
                    }
                    .padding()
                }
            }

            Divider()

            footer
        }
        .frame(minWidth: 400, minHeight: 450)
        .task {
            await loadData()
        }
    }

    // MARK: - Header

    private var header: some View {
        HStack {
            VStack(alignment: .leading, spacing: 2) {
                Text("Organize Chat")
                    .font(.headline)
                Text(session.displayTitle)
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
            }
            Spacer()
        }
        .padding()
    }

    // MARK: - Loading State

    private var loadingState: some View {
        VStack {
            LoadingIndicator(style: .large)
            Text("Loading...")
                .font(HygurTypography.subheadline)
                .foregroundStyle(HygurColors.textSecondary)
                .padding(.top, HygurSpacing.sm)
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
    }

    // MARK: - Error State

    private func errorState(_ message: String) -> some View {
        EmptyStateView(
            icon: "exclamationmark.triangle",
            title: "Failed to load data",
            subtitle: message,
            action: ("Retry", { Task { await loadData() } })
        )
    }

    // MARK: - Project Section

    private var projectSection: some View {
        VStack(alignment: .leading, spacing: 8) {
            Label("Project", systemImage: "folder")
                .font(.subheadline)
                .foregroundStyle(.secondary)

            Picker("Project", selection: $selectedProjectId) {
                Text("None")
                    .tag(nil as String?)

                ForEach(projects.filter { !$0.archived }) { project in
                    HStack {
                        Text(project.name)
                        if project.itemCount > 0 {
                            Text("(\(project.itemCount) items)")
                                .foregroundStyle(.secondary)
                        }
                    }
                    .tag(project.id as String?)
                }
            }
            .pickerStyle(.menu)

            if let projectId = selectedProjectId,
               let project = projects.first(where: { $0.id == projectId }) {
                HStack(spacing: 8) {
                    Image(systemName: "folder.fill")
                        .foregroundStyle(.orange)
                    VStack(alignment: .leading, spacing: 2) {
                        Text(project.name)
                            .font(.body)
                        if let description = project.description, !description.isEmpty {
                            Text(description)
                                .font(.caption)
                                .foregroundStyle(.secondary)
                                .lineLimit(2)
                        }
                    }
                }
                .padding(HygurSpacing.sm)
                .background(HygurColors.textSecondary.opacity(0.1))
                .clipShape(RoundedRectangle(cornerRadius: HygurRadius.md))
            }
        }
    }

    // MARK: - Tags Section

    private var tagsSection: some View {
        VStack(alignment: .leading, spacing: 8) {
            Label("Tags", systemImage: "tag")
                .font(.subheadline)
                .foregroundStyle(.secondary)

            if tags.isEmpty {
                Text("No tags available")
                    .font(.caption)
                    .foregroundStyle(.tertiary)
                    .padding(.vertical, 8)
            } else {
                FlowLayout(spacing: 8) {
                    ForEach(tags) { tag in
                        TagToggleButton(
                            tag: tag,
                            isSelected: selectedTagIds.contains(tag.id)
                        ) {
                            if selectedTagIds.contains(tag.id) {
                                selectedTagIds.remove(tag.id)
                            } else {
                                selectedTagIds.insert(tag.id)
                            }
                        }
                    }
                }
            }

            if !selectedTagIds.isEmpty {
                HStack {
                    Text("\(selectedTagIds.count) tag\(selectedTagIds.count > 1 ? "s" : "") selected")
                        .font(.caption)
                        .foregroundStyle(.secondary)
                    Spacer()
                    Button("Clear all") {
                        selectedTagIds.removeAll()
                    }
                    .font(HygurTypography.caption)
                    .buttonStyle(.plain)
                    .foregroundStyle(HygurColors.accent)
                }
                .padding(.top, 4)
            }
        }
    }

    // MARK: - Footer

    private var footer: some View {
        HStack {
            Button("Cancel") {
                dismiss()
            }
            .keyboardShortcut(.escape)

            Spacer()

            Button("Save") {
                saveChanges()
            }
            .buttonStyle(.borderedProminent)
            .keyboardShortcut(.return)
        }
        .padding()
    }

    // MARK: - Actions

    private func loadData() async {
        isLoading = true
        errorMessage = nil
        defer { isLoading = false }

        do {
            async let projectsTask = sidecar.listProjects()
            async let tagsTask = sidecar.listTags()

            let (loadedProjects, loadedTags) = try await (projectsTask, tagsTask)

            projects = loadedProjects
            tags = loadedTags

            selectedProjectId = session.projectId
            selectedTagIds = Set(session.tagIds)
        } catch {
            errorMessage = error.localizedDescription
        }
    }

    private func saveChanges() {
        if selectedProjectId != session.projectId {
            onUpdateProject?(selectedProjectId)
        }

        let currentTagIds = Set(session.tagIds)
        if selectedTagIds != currentTagIds {
            onUpdateTags?(Array(selectedTagIds))
        }

        dismiss()
    }
}

// MARK: - Tag Toggle Button

struct TagToggleButton: View {
    let tag: Tag
    let isSelected: Bool
    let action: () -> Void

    var body: some View {
        Button(action: action) {
            HStack(spacing: 4) {
                Circle()
                    .fill(tag.swiftUIColor)
                    .frame(width: 8, height: 8)
                Text(tag.name)
                    .font(.caption)
                if isSelected {
                    Image(systemName: "checkmark")
                        .font(.caption2)
                }
            }
            .padding(.horizontal, HygurSpacing.sm + 2)
            .padding(.vertical, HygurSpacing.sm - 2)
            .background(isSelected ? tag.swiftUIColor.opacity(0.2) : HygurColors.textSecondary.opacity(0.1))
            .foregroundStyle(isSelected ? tag.swiftUIColor : HygurColors.textPrimary)
            .clipShape(RoundedRectangle(cornerRadius: HygurRadius.xl))
            .overlay(
                RoundedRectangle(cornerRadius: HygurRadius.xl)
                    .strokeBorder(isSelected ? tag.swiftUIColor : Color.clear, lineWidth: 1)
            )
        }
        .buttonStyle(.plain)
    }
}

// MARK: - Preview

#Preview {
    ChatOrganizeSheet(
        session: ChatSession(
            title: "Test Chat",
            projectId: nil,
            tagIds: []
        ),
        onUpdateProject: { _ in },
        onUpdateTags: { _ in }
    )
}
