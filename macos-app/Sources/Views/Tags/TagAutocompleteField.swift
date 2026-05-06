import SwiftUI

/// Type-ahead tag picker. Shows the currently-selected tags as removable
/// pills, plus a search field that filters `availableTags` and exposes the
/// matches as a popover. Hitting Return on the field selects the highlighted
/// suggestion or, if there is no match, signals the caller to create a new tag.
///
/// This is shared between CreateNoteView and EditNoteView so both modals get
/// the same autocomplete UX. Project-driven auto-tagging is handled by the
/// owning view (it watches `selectedProjectId` and inserts the project's tags),
/// keeping this component focused on filtering/selection.
struct TagAutocompleteField: View {
    @Binding var selectedTags: [Tag]
    let availableTags: [Tag]
    /// Optional callback invoked when the user types a name that doesn't match
    /// any existing tag and presses Return. If nil, no-match Returns are ignored.
    var onCreate: ((String) -> Void)?

    @State private var query = ""
    @State private var highlightedIndex = 0
    @FocusState private var isFocused: Bool

    var body: some View {
        VStack(alignment: .leading, spacing: HygurSpacing.sm) {
            if !selectedTags.isEmpty {
                FlowLayout(spacing: HygurSpacing.sm) {
                    ForEach(selectedTags) { tag in
                        TagPillView(tag: tag, showRemoveButton: true) {
                            selectedTags.removeAll { $0.id == tag.id }
                        }
                    }
                }
            }

            searchField
        }
    }

    // MARK: - Search field

    private var searchField: some View {
        VStack(alignment: .leading, spacing: 0) {
            HStack(spacing: HygurSpacing.sm) {
                Image(systemName: "magnifyingglass")
                    .foregroundStyle(HygurColors.textTertiary)
                TextField("Search or create a tag…", text: $query)
                    .textFieldStyle(.plain)
                    .focused($isFocused)
                    .onSubmit(commitCurrent)
                    .onKeyPress(.upArrow) {
                        moveHighlight(by: -1)
                        return .handled
                    }
                    .onKeyPress(.downArrow) {
                        moveHighlight(by: 1)
                        return .handled
                    }
                    .onChange(of: query) { _, _ in
                        // Keep the highlight inside the new suggestion list
                        // so arrow nav doesn't crash on filtered results.
                        highlightedIndex = min(highlightedIndex, max(0, suggestions.count - 1))
                    }
            }
            .padding(.horizontal, HygurSpacing.sm)
            .padding(.vertical, 6)
            .background(
                RoundedRectangle(cornerRadius: HygurRadius.sm)
                    .fill(HygurColors.surface)
            )
            .overlay(
                RoundedRectangle(cornerRadius: HygurRadius.sm)
                    .strokeBorder(
                        isFocused ? HygurColors.accent.opacity(0.6) : HygurColors.border,
                        lineWidth: isFocused ? 1.5 : 1
                    )
            )

            if isFocused && !suggestions.isEmpty {
                suggestionList
                    .padding(.top, 4)
            } else if isFocused, !query.isEmpty, onCreate != nil {
                createHint
                    .padding(.top, 4)
            }
        }
    }

    @ViewBuilder
    private var suggestionList: some View {
        VStack(alignment: .leading, spacing: 0) {
            ForEach(Array(suggestions.enumerated()), id: \.element.id) { index, tag in
                Button {
                    select(tag)
                } label: {
                    HStack(spacing: HygurSpacing.sm) {
                        Circle()
                            .fill(tag.swiftUIColor)
                            .frame(width: 10, height: 10)
                        Text(tag.name)
                            .font(HygurTypography.body)
                        Spacer()
                        if tag.usageCount > 0 {
                            Text("\(tag.usageCount)")
                                .font(HygurTypography.caption)
                                .foregroundStyle(HygurColors.textTertiary)
                        }
                    }
                    .padding(.horizontal, HygurSpacing.sm)
                    .padding(.vertical, 6)
                    .background(
                        RoundedRectangle(cornerRadius: HygurRadius.sm)
                            .fill(index == highlightedIndex ? HygurColors.accent.opacity(0.15) : Color.clear)
                    )
                    .contentShape(Rectangle())
                }
                .buttonStyle(.plain)
                .onHover { hovering in
                    if hovering { highlightedIndex = index }
                }
            }

            // If the typed name doesn't match any visible suggestion, show a
            // "Create" affordance so the user can finalise the new tag with Return.
            if onCreate != nil, !query.trimmingCharacters(in: .whitespaces).isEmpty,
               !suggestions.contains(where: { $0.name.caseInsensitiveCompare(query) == .orderedSame }) {
                Divider().padding(.vertical, 4)
                createRow
            }
        }
        .padding(4)
        .background(
            RoundedRectangle(cornerRadius: HygurRadius.sm)
                .fill(HygurColors.surfaceElevated)
        )
        .overlay(
            RoundedRectangle(cornerRadius: HygurRadius.sm)
                .strokeBorder(HygurColors.border, lineWidth: 0.5)
        )
        .frame(maxHeight: 220)
    }

    private var createHint: some View {
        Button(action: createCurrent) {
            HStack(spacing: HygurSpacing.sm) {
                Image(systemName: "plus.circle")
                    .foregroundStyle(HygurColors.accent)
                Text("Create tag “\(query)”")
                    .font(HygurTypography.body)
                Spacer()
                Text("⏎")
                    .font(HygurTypography.caption)
                    .foregroundStyle(HygurColors.textTertiary)
            }
            .padding(.horizontal, HygurSpacing.sm)
            .padding(.vertical, 6)
            .background(
                RoundedRectangle(cornerRadius: HygurRadius.sm)
                    .fill(HygurColors.surfaceElevated)
            )
            .overlay(
                RoundedRectangle(cornerRadius: HygurRadius.sm)
                    .strokeBorder(HygurColors.accent.opacity(0.3), lineWidth: 0.5)
            )
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
    }

    private var createRow: some View {
        Button(action: createCurrent) {
            HStack(spacing: HygurSpacing.sm) {
                Image(systemName: "plus.circle.fill")
                    .foregroundStyle(HygurColors.accent)
                Text("Create “\(query)”")
                    .font(HygurTypography.body)
                Spacer()
            }
            .padding(.horizontal, HygurSpacing.sm)
            .padding(.vertical, 6)
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
    }

    // MARK: - Logic

    /// Tags that match the current query and are not already selected.
    private var suggestions: [Tag] {
        let normalized = query.trimmingCharacters(in: .whitespaces).lowercased()
        let unselected = availableTags.filter { tag in
            !selectedTags.contains(where: { $0.id == tag.id })
        }
        guard !normalized.isEmpty else {
            return Array(unselected.prefix(8))
        }
        return unselected
            .filter { $0.name.lowercased().contains(normalized) }
            .prefix(8)
            .map { $0 }
    }

    private func moveHighlight(by delta: Int) {
        guard !suggestions.isEmpty else { return }
        let next = highlightedIndex + delta
        highlightedIndex = max(0, min(next, suggestions.count - 1))
    }

    private func commitCurrent() {
        if !suggestions.isEmpty {
            select(suggestions[highlightedIndex])
        } else {
            createCurrent()
        }
    }

    private func select(_ tag: Tag) {
        if !selectedTags.contains(where: { $0.id == tag.id }) {
            selectedTags.append(tag)
        }
        query = ""
        highlightedIndex = 0
    }

    private func createCurrent() {
        let trimmed = query.trimmingCharacters(in: .whitespaces)
        guard !trimmed.isEmpty, let onCreate else { return }
        onCreate(trimmed)
        query = ""
        highlightedIndex = 0
    }
}
