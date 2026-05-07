import SwiftUI

/// Spotlight-style command palette presented as a sheet. Caller supplies the
/// `ChatSessionManager` (so recent chat sessions surface as commands) and an
/// `onExecute` closure that ContentView uses to actually dispatch the action.
struct CommandPaletteView: View {
    let sessionManager: ChatSessionManager?
    let onExecute: (CommandAction) -> Void
    let onDismiss: () -> Void

    @State private var viewModel: CommandPaletteViewModel
    @FocusState private var isSearchFocused: Bool

    init(
        sessionManager: ChatSessionManager?,
        onExecute: @escaping (CommandAction) -> Void,
        onDismiss: @escaping () -> Void
    ) {
        self.sessionManager = sessionManager
        self.onExecute = onExecute
        self.onDismiss = onDismiss
        _viewModel = State(initialValue: CommandPaletteViewModel(sessionManager: sessionManager))
    }

    var body: some View {
        // Backdrop catches clicks outside the palette and dismisses it. On
        // macOS sheets are modal and ignore outside taps by default; rendering
        // the palette inside its own overlay lets us implement the standard
        // Spotlight behaviour the user expects.
        ZStack {
            Color.black.opacity(0.18)
                .ignoresSafeArea()
                .contentShape(Rectangle())
                .onTapGesture { onDismiss() }

            VStack(spacing: 0) {
                searchBar
                Divider()
                resultsList
            }
            .frame(width: 600, height: 420)
            .background(HygurColors.surface)
            .clipShape(RoundedRectangle(cornerRadius: HygurRadius.lg))
            .overlay(
                RoundedRectangle(cornerRadius: HygurRadius.lg)
                    .strokeBorder(HygurColors.brandBlue.opacity(0.18), lineWidth: 1)
            )
            .shadow(color: .black.opacity(0.35), radius: 32, x: 0, y: 12)
            // Block backdrop taps from passing through the palette itself.
            .contentShape(RoundedRectangle(cornerRadius: HygurRadius.lg))
            .onTapGesture { /* swallow */ }
        }
        .onAppear { isSearchFocused = true }
    }

    // MARK: - Search Bar

    private var searchBar: some View {
        HStack(spacing: HygurSpacing.md) {
            Image(systemName: "magnifyingglass")
                .font(.title3)
                .foregroundStyle(HygurColors.textSecondary)
            TextField("Type a command, a project, or a chat title…", text: $viewModel.query)
                .textFieldStyle(.plain)
                .font(.title3)
                .focused($isSearchFocused)
                .onSubmit(executeSelected)
                .onKeyPress(.upArrow) {
                    viewModel.moveSelection(by: -1)
                    return .handled
                }
                .onKeyPress(.downArrow) {
                    viewModel.moveSelection(by: 1)
                    return .handled
                }
                .onKeyPress(.escape) {
                    onDismiss()
                    return .handled
                }
        }
        .padding(HygurSpacing.lg)
    }

    // MARK: - Results

    private var resultsList: some View {
        ScrollViewReader { proxy in
            ScrollView {
                LazyVStack(spacing: 0) {
                    let commands = viewModel.filteredCommands
                    if commands.isEmpty {
                        emptyState
                            .padding(.vertical, HygurSpacing.xxl)
                    } else {
                        ForEach(Array(commands.enumerated()), id: \.element.id) { index, command in
                            CommandRow(
                                command: command,
                                isSelected: index == viewModel.selectedIndex
                            )
                            .id(command.id)
                            .contentShape(Rectangle())
                            .onTapGesture {
                                viewModel.setSelection(to: index)
                                executeSelected()
                            }
                            .onHover { hovering in
                                if hovering { viewModel.setSelection(to: index) }
                            }
                        }
                    }
                }
            }
            .onChange(of: viewModel.selectedIndex) { _, _ in
                if let cmd = viewModel.selectedCommand {
                    withAnimation(.linear(duration: 0.05)) {
                        proxy.scrollTo(cmd.id, anchor: .center)
                    }
                }
            }
        }
    }

    private var emptyState: some View {
        VStack(spacing: HygurSpacing.sm) {
            Image(systemName: "questionmark.circle")
                .font(.title)
                .foregroundStyle(HygurColors.textTertiary)
            Text("No matching commands")
                .font(.subheadline)
                .foregroundStyle(HygurColors.textSecondary)
        }
        .frame(maxWidth: .infinity)
    }

    // MARK: - Actions

    private func executeSelected() {
        guard let command = viewModel.selectedCommand else { return }
        onExecute(command.action)
    }
}

private struct CommandRow: View {
    let command: PaletteCommand
    let isSelected: Bool

    var body: some View {
        HStack(spacing: HygurSpacing.md) {
            Image(systemName: command.icon)
                .font(.title3)
                .frame(width: 24)
                .foregroundStyle(isSelected ? HygurColors.accent : HygurColors.textSecondary)
            VStack(alignment: .leading, spacing: 2) {
                Text(command.title)
                    .font(.body)
                    .foregroundStyle(HygurColors.textPrimary)
                if let subtitle = command.subtitle, !subtitle.isEmpty {
                    Text(subtitle)
                        .font(.caption)
                        .foregroundStyle(HygurColors.textSecondary)
                        .lineLimit(1)
                }
            }
            Spacer()
            if isSelected {
                Image(systemName: "return")
                    .font(.caption)
                    .foregroundStyle(HygurColors.textTertiary)
            }
        }
        .padding(.horizontal, HygurSpacing.lg)
        .padding(.vertical, HygurSpacing.sm + 2)
        .background(
            isSelected
                ? HygurColors.accent.opacity(0.15)
                : Color.clear
        )
    }
}
