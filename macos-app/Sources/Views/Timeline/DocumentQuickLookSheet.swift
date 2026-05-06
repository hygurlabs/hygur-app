import SwiftUI

/// QuickLook-style sheet showing the full content of a knowledge item
/// with inline tag, project and note editing.
/// Usable from both the Timeline and the Knowledge Base.
struct DocumentQuickLookSheet: View {
    let contentId: String
    /// Shown in the header before the full item is fetched.
    var displayTitle: String? = nil
    var displaySourceType: String? = nil
    var displayDate: String? = nil

    @Environment(\.dismiss) private var dismiss
    @State private var viewModel = DocumentQuickLookViewModel()

    var body: some View {
        VStack(spacing: 0) {
            header
            Divider()
            if viewModel.isLoading {
                Spacer()
                ProgressView("Loading…").padding()
                Spacer()
            } else if let err = viewModel.error {
                Spacer()
                EmptyStateView(icon: "exclamationmark.triangle", title: "Error", subtitle: err)
                Spacer()
            } else {
                mainContent
            }
        }
        .frame(minWidth: 900, idealWidth: 1000, minHeight: 600, idealHeight: 700)
        .task { await viewModel.load(contentId: contentId) }
    }

    // MARK: - Header

    private var header: some View {
        HStack(spacing: HygurSpacing.md) {
            let sourceType = viewModel.item?.sourceType ?? displaySourceType ?? ""
            Image(systemName: HygurColors.sourceTypeIcon(sourceType))
                .foregroundStyle(HygurColors.sourceTypeColor(sourceType))
                .font(.title3)

            VStack(alignment: .leading, spacing: 2) {
                Text(viewModel.item?.title ?? displayTitle ?? contentId)
                    .font(HygurTypography.headline)
                    .lineLimit(1)

                Text(subtitleText)
                    .font(HygurTypography.caption)
                    .foregroundStyle(HygurColors.textSecondary)
            }

            Spacer()

            let st = viewModel.item?.sourceType ?? displaySourceType
            if let st {
                Text(st.uppercased())
                    .font(HygurTypography.captionMono)
                    .padding(.horizontal, HygurSpacing.sm)
                    .padding(.vertical, 3)
                    .background(HygurColors.sourceTypeColor(st).opacity(0.12), in: Capsule())
                    .foregroundStyle(HygurColors.sourceTypeColor(st))
            }

            Button { dismiss() } label: {
                Image(systemName: "xmark.circle.fill")
                    .foregroundStyle(HygurColors.textSecondary)
                    .font(.title3)
            }
            .buttonStyle(.plain)
            .keyboardShortcut(.escape, modifiers: [])
        }
        .padding(HygurSpacing.lg)
    }

    private var subtitleText: String {
        var parts: [String] = []
        if let d = displayDate ?? viewModel.item?.date { parts.append(d) }
        if let s = viewModel.item?.sourceType ?? displaySourceType { parts.append(s) }
        return parts.joined(separator: " · ")
    }

    // MARK: - Main content (2-column layout)

    private var mainContent: some View {
        HStack(spacing: 0) {
            textColumn
                .frame(minWidth: 0, maxWidth: .infinity)
            Divider()
            enrichmentPanel
                .frame(width: 360)
        }
    }

    // MARK: - Text column

    private var textColumn: some View {
        Group {
            if viewModel.fullText.isEmpty {
                VStack(spacing: HygurSpacing.sm) {
                    Image(systemName: "doc.text")
                        .font(.largeTitle)
                        .foregroundStyle(HygurColors.textTertiary)
                    Text("Content unavailable")
                        .font(HygurTypography.body)
                        .foregroundStyle(HygurColors.textSecondary)
                    Text("The full text of this document is not indexed.")
                        .font(HygurTypography.caption)
                        .foregroundStyle(HygurColors.textTertiary)
                        .multilineTextAlignment(.center)
                }
                .frame(maxWidth: .infinity, maxHeight: .infinity)
            } else {
                ScrollView {
                    Text(viewModel.fullText)
                        .font(HygurTypography.body)
                        .foregroundStyle(HygurColors.textPrimary)
                        .textSelection(.enabled)
                        .frame(maxWidth: .infinity, alignment: .leading)
                        .padding(HygurSpacing.lg)
                }
            }
        }
    }

    // MARK: - Enrichment panel

    private var enrichmentPanel: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: HygurSpacing.xl) {
                tagsSection
                Divider()
                projectSection
                Divider()
                noteSection
            }
            .padding(HygurSpacing.lg)
        }
    }

    // MARK: - Tags section

    private var tagsSection: some View {
        VStack(alignment: .leading, spacing: HygurSpacing.sm) {
            Text("Tags")
                .font(HygurTypography.subheadline)
                .foregroundStyle(HygurColors.textSecondary)

            if viewModel.isLoadingTags {
                HStack(spacing: HygurSpacing.xs) {
                    ProgressView().controlSize(.mini)
                    Text("Loading…").font(HygurTypography.caption).foregroundStyle(HygurColors.textTertiary)
                }
            } else {
                if !viewModel.currentTags.isEmpty {
                    VStack(alignment: .leading, spacing: HygurSpacing.xs) {
                        Text("Active").font(HygurTypography.caption).foregroundStyle(HygurColors.textTertiary)
                        QuickLookTagsFlow(spacing: 6) {
                            ForEach(viewModel.currentTags) { tag in
                                TagPillView(tag: tag, showRemoveButton: true) {
                                    Task { await viewModel.removeTag(tag) }
                                }
                                .opacity(viewModel.isSavingTag ? 0.5 : 1)
                            }
                        }
                    }
                }

                if !viewModel.unassignedTags.isEmpty {
                    VStack(alignment: .leading, spacing: HygurSpacing.xs) {
                        Text("Add").font(HygurTypography.caption).foregroundStyle(HygurColors.textTertiary)
                        QuickLookTagsFlow(spacing: 6) {
                            ForEach(viewModel.unassignedTags) { tag in
                                SelectableTagPillView(tag: tag, isSelected: false) {
                                    Task { await viewModel.addTag(tag) }
                                }
                                .disabled(viewModel.isSavingTag)
                            }
                        }
                    }
                }

                if viewModel.currentTags.isEmpty && viewModel.unassignedTags.isEmpty {
                    Text("No tags created yet.")
                        .font(HygurTypography.caption)
                        .foregroundStyle(HygurColors.textTertiary)
                }
            }
        }
    }

    // MARK: - Project section

    private var projectSection: some View {
        VStack(alignment: .leading, spacing: HygurSpacing.sm) {
            Text("Project")
                .font(HygurTypography.subheadline)
                .foregroundStyle(HygurColors.textSecondary)

            if viewModel.isLoadingProjects {
                HStack(spacing: HygurSpacing.xs) {
                    ProgressView().controlSize(.mini)
                    Text("Loading…").font(HygurTypography.caption).foregroundStyle(HygurColors.textTertiary)
                }
            } else {
                HStack {
                    Picker("Project", selection: $viewModel.selectedProjectId) {
                        Text("None").tag(String?.none)
                        ForEach(viewModel.projects) { project in
                            Text(project.name).tag(String?.some(project.id))
                        }
                    }
                    .labelsHidden()
                    .pickerStyle(.menu)
                    .disabled(viewModel.isSavingProject)

                    if viewModel.projectChanged {
                        Button { Task { await viewModel.saveProject() } } label: {
                            if viewModel.isSavingProject {
                                ProgressView().controlSize(.mini)
                            } else {
                                Image(systemName: "checkmark.circle.fill").foregroundStyle(.green)
                            }
                        }
                        .buttonStyle(.plain)

                        Button { viewModel.revertProject() } label: {
                            Image(systemName: "xmark.circle.fill").foregroundStyle(HygurColors.textSecondary)
                        }
                        .buttonStyle(.plain)
                    }
                }
            }
        }
    }

    // MARK: - Note section

    private var noteSection: some View {
        VStack(alignment: .leading, spacing: HygurSpacing.sm) {
            Text("Add a note")
                .font(HygurTypography.subheadline)
                .foregroundStyle(HygurColors.textSecondary)

            TextField("Title", text: $viewModel.noteTitle)
                .textFieldStyle(.roundedBorder)
                .font(HygurTypography.body)

            TextEditor(text: $viewModel.noteContent)
                .font(HygurTypography.body)
                .frame(minHeight: 100)
                .overlay(
                    RoundedRectangle(cornerRadius: HygurRadius.sm)
                        .stroke(HygurColors.border, lineWidth: 1)
                )

            HStack {
                if viewModel.noteSaved {
                    Label("Note saved", systemImage: "checkmark.circle.fill")
                        .font(HygurTypography.caption)
                        .foregroundStyle(.green)
                }
                Spacer()
                Button {
                    Task { await viewModel.createLinkedNote() }
                } label: {
                    if viewModel.isSavingNote {
                        ProgressView().controlSize(.mini)
                    } else {
                        Label("Save", systemImage: "arrow.up.circle.fill")
                    }
                }
                .buttonStyle(.borderedProminent)
                .disabled(
                    viewModel.isSavingNote
                    || viewModel.noteTitle.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
                    || viewModel.noteContent.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
                )
            }
        }
    }
}

// MARK: - Flow layout (local to this file)

private struct QuickLookTagsFlow: Layout {
    var spacing: CGFloat = 6

    func sizeThatFits(proposal: ProposedViewSize, subviews: Subviews, cache: inout ()) -> CGSize {
        layout(proposal: proposal, subviews: subviews).size
    }

    func placeSubviews(in bounds: CGRect, proposal: ProposedViewSize, subviews: Subviews, cache: inout ()) {
        let result = layout(proposal: proposal, subviews: subviews)
        for (index, pos) in result.positions.enumerated() {
            subviews[index].place(
                at: CGPoint(x: bounds.minX + pos.x, y: bounds.minY + pos.y),
                proposal: .unspecified
            )
        }
    }

    private func layout(proposal: ProposedViewSize, subviews: Subviews) -> (size: CGSize, positions: [CGPoint]) {
        let maxWidth = proposal.width ?? 300
        var positions: [CGPoint] = []
        var x: CGFloat = 0, y: CGFloat = 0, lineH: CGFloat = 0
        for subview in subviews {
            let sz = subview.sizeThatFits(.unspecified)
            if x + sz.width > maxWidth && x > 0 { x = 0; y += lineH + spacing; lineH = 0 }
            positions.append(CGPoint(x: x, y: y))
            x += sz.width + spacing
            lineH = max(lineH, sz.height)
        }
        return (CGSize(width: maxWidth, height: y + lineH), positions)
    }
}

#Preview {
    DocumentQuickLookSheet(
        contentId: "test-123",
        displayTitle: "Sample document",
        displaySourceType: "note",
        displayDate: "2026-05-01"
    )
}
