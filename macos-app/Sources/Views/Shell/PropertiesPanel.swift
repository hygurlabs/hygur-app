import SwiftUI

/// Right-hand inspector panel — surfaces metadata for the current selection.
/// Phase 3 ships the host + visibility toggle; per-type inspectors
/// (NotePropertiesView, ProjectPropertiesView, …) arrive in Phase 6.
///
/// Visibility is persisted via `@AppStorage("hygur.properties.visible")` so
/// users get the same layout on relaunch.
struct PropertiesPanel: View {
    let selection: SidebarItem?
    @Environment(InspectorSelection.self) private var inspector

    var body: some View {
        VStack(alignment: .leading, spacing: HygurSpacing.sm) {
            HStack {
                Text("Properties")
                    .font(HygurTypography.subheadline)
                    .foregroundStyle(HygurColors.textSecondary)
                Spacer()
            }
            Divider()

            inspectorBody
                .frame(maxHeight: .infinity)
        }
        .padding(HygurSpacing.md)
        .frame(minWidth: 220, idealWidth: 260, maxWidth: 320, maxHeight: .infinity)
        .background(.regularMaterial)
        .overlay(alignment: .leading) {
            Rectangle()
                .fill(HygurColors.divider)
                .frame(width: 0.5)
        }
    }

    @ViewBuilder
    private var inspectorBody: some View {
        // Inspector selection (set by single-click in Notes / KB / Mail)
        // takes priority; sidebar deep-link is the fallback so Favorites
        // → note(id) still populates the panel directly.
        if let entity = inspector.current {
            switch entity {
            case .note(let id):
                NotePropertiesView(noteId: id)
            case .knowledgeItem(let id):
                KnowledgeItemPropertiesView(contentId: id)
            case .mailThread(let thread):
                MailThreadPropertiesView(thread: thread)
            case .project:
                EmptyPropertiesPlaceholder()
            }
        } else {
            switch selection {
            case .note(let id):
                NotePropertiesView(noteId: id)
            default:
                EmptyPropertiesPlaceholder()
            }
        }
    }
}

private struct EmptyPropertiesPlaceholder: View {
    var body: some View {
        VStack(spacing: HygurSpacing.sm) {
            Image(systemName: "info.circle")
                .font(.system(size: 22, weight: .light))
                .foregroundStyle(HygurColors.textTertiary)
            Text("Select something to see its properties.")
                .font(HygurTypography.caption)
                .foregroundStyle(HygurColors.textTertiary)
                .multilineTextAlignment(.center)
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
    }
}
