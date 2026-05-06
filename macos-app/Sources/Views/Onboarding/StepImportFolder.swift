import SwiftUI
import AppKit

/// Step 4 of the onboarding flow — let the user point Hygur at a folder so the
/// knowledge base has *something* to search before they reach the chat. The
/// actual ingestion runs through the same `KnowledgeBaseViewModel` the main
/// view uses; we just front it with a much simpler picker / progress UI.
///
/// Skip is always available (this step is explicitly optional). Once an
/// import succeeds we surface a confirmation row but don't auto-advance — the
/// user keeps control of when they move on.
struct StepImportFolder: View {
    @State private var viewModel = KnowledgeBaseViewModel()
    @State private var pickedFolder: URL?
    @State private var importedCount: Int = 0
    @State private var didImport: Bool = false

    var body: some View {
        VStack(spacing: HygurSpacing.lg) {
            header

            VStack(spacing: HygurSpacing.md) {
                if let progress = viewModel.importProgress {
                    progressCard(progress)
                } else if didImport, let folder = pickedFolder {
                    successCard(folder: folder)
                } else if let folder = pickedFolder {
                    selectedFolderCard(folder)
                } else {
                    dropZone
                }

                if let error = viewModel.error {
                    errorBanner(error)
                }
            }
            .padding(.horizontal, HygurSpacing.xxxl)
            .frame(maxWidth: 580)

            Spacer()
        }
        .padding(.top, HygurSpacing.xl)
        .padding(.bottom, HygurSpacing.lg)
        .frame(maxWidth: .infinity, maxHeight: .infinity)
    }

    // MARK: - Layout

    private var header: some View {
        VStack(spacing: HygurSpacing.sm) {
            Image(systemName: "folder.fill.badge.plus")
                .font(.system(size: 36, weight: .light))
                .foregroundStyle(HygurColors.accent)
            Text("Import your first folder")
                .font(HygurTypography.title)
                .foregroundStyle(HygurColors.textPrimary)
            Text("Optional — point Hygur at a folder of PDFs, Word documents, Markdown or notes. Files are indexed in the background and stay on this Mac.")
                .font(HygurTypography.body)
                .foregroundStyle(HygurColors.textSecondary)
                .multilineTextAlignment(.center)
                .frame(maxWidth: 480)
        }
    }

    private var dropZone: some View {
        VStack(spacing: HygurSpacing.md) {
            Image(systemName: "tray.and.arrow.down")
                .font(.system(size: 32, weight: .light))
                .foregroundStyle(HygurColors.textTertiary)
            Text("Drop a folder here")
                .font(HygurTypography.subheadline.weight(.medium))
                .foregroundStyle(HygurColors.textPrimary)
            Text("or click below to pick one")
                .font(HygurTypography.caption)
                .foregroundStyle(HygurColors.textSecondary)
            Button("Choose folder…", action: pickFolder)
                .buttonStyle(.borderedProminent)
                .controlSize(.regular)
                .tint(HygurColors.accent)
        }
        .frame(maxWidth: .infinity)
        .padding(.vertical, HygurSpacing.xxl)
        .background(
            RoundedRectangle(cornerRadius: HygurRadius.lg, style: .continuous)
                .fill(HygurColors.surface)
        )
        .overlay(
            RoundedRectangle(cornerRadius: HygurRadius.lg, style: .continuous)
                .strokeBorder(HygurColors.border, style: StrokeStyle(lineWidth: 1, dash: [6, 4]))
        )
        .onDrop(of: [.fileURL], isTargeted: nil) { providers in
            handleDrop(providers)
        }
    }

    private func selectedFolderCard(_ folder: URL) -> some View {
        VStack(alignment: .leading, spacing: HygurSpacing.md) {
            HStack(spacing: HygurSpacing.md) {
                Image(systemName: "folder.fill")
                    .font(.system(size: 22))
                    .foregroundStyle(HygurColors.accent)
                VStack(alignment: .leading, spacing: 2) {
                    Text(folder.lastPathComponent)
                        .font(HygurTypography.subheadline.weight(.medium))
                        .foregroundStyle(HygurColors.textPrimary)
                    Text(folder.path)
                        .font(HygurTypography.caption)
                        .foregroundStyle(HygurColors.textTertiary)
                        .lineLimit(1)
                        .truncationMode(.middle)
                }
                Spacer()
                Button("Change") { pickFolder() }
                    .buttonStyle(.borderless)
                    .controlSize(.small)
            }
            HStack {
                Spacer()
                Button("Import") {
                    Task { await runImport(folder: folder) }
                }
                .buttonStyle(.borderedProminent)
                .controlSize(.regular)
                .tint(HygurColors.accent)
            }
        }
        .padding(HygurSpacing.lg)
        .background(
            RoundedRectangle(cornerRadius: HygurRadius.lg, style: .continuous)
                .fill(HygurColors.surface)
        )
    }

    private func progressCard(_ progress: KnowledgeBaseViewModel.ImportProgress) -> some View {
        VStack(alignment: .leading, spacing: HygurSpacing.sm) {
            HStack(spacing: HygurSpacing.sm) {
                ProgressView().controlSize(.small)
                Text("Importing…")
                    .font(HygurTypography.subheadline.weight(.medium))
                    .foregroundStyle(HygurColors.textPrimary)
                Spacer()
                Text("\(progress.current)/\(progress.total)")
                    .font(HygurTypography.caption)
                    .foregroundStyle(HygurColors.textSecondary)
            }
            Text(progress.currentFileName)
                .font(HygurTypography.caption)
                .foregroundStyle(HygurColors.textTertiary)
                .lineLimit(1)
                .truncationMode(.middle)
            if progress.total > 0 {
                ProgressView(value: Double(progress.current), total: Double(max(progress.total, 1)))
                    .tint(HygurColors.accent)
            }
        }
        .padding(HygurSpacing.lg)
        .background(
            RoundedRectangle(cornerRadius: HygurRadius.lg, style: .continuous)
                .fill(HygurColors.surface)
        )
    }

    private func successCard(folder: URL) -> some View {
        HStack(spacing: HygurSpacing.md) {
            Image(systemName: "checkmark.seal.fill")
                .font(.system(size: 22))
                .foregroundStyle(HygurColors.success)
            VStack(alignment: .leading, spacing: 2) {
                Text("Folder queued for indexing")
                    .font(HygurTypography.subheadline.weight(.medium))
                    .foregroundStyle(HygurColors.textPrimary)
                Text("\(folder.lastPathComponent) — \(importedCount) document\(importedCount == 1 ? "" : "s") in the knowledge base.")
                    .font(HygurTypography.caption)
                    .foregroundStyle(HygurColors.textSecondary)
            }
            Spacer()
            Button("Pick another") { pickFolder() }
                .buttonStyle(.borderless)
                .controlSize(.small)
        }
        .padding(HygurSpacing.lg)
        .background(
            RoundedRectangle(cornerRadius: HygurRadius.lg, style: .continuous)
                .fill(HygurColors.surface)
        )
    }

    private func errorBanner(_ message: String) -> some View {
        HStack(spacing: HygurSpacing.sm) {
            Image(systemName: "exclamationmark.triangle.fill")
                .foregroundStyle(HygurColors.danger)
            Text(message)
                .font(HygurTypography.caption)
                .foregroundStyle(HygurColors.textSecondary)
            Spacer()
            Button("Dismiss") { viewModel.error = nil }
                .buttonStyle(.borderless)
                .font(HygurTypography.caption)
        }
        .padding(HygurSpacing.md)
        .background(
            RoundedRectangle(cornerRadius: 8, style: .continuous)
                .fill(HygurColors.surface)
        )
    }

    // MARK: - Actions

    private func pickFolder() {
        let panel = NSOpenPanel()
        panel.canChooseFiles = false
        panel.canChooseDirectories = true
        panel.allowsMultipleSelection = false
        panel.message = "Select a folder to import"
        panel.prompt = "Choose"

        if panel.runModal() == .OK, let url = panel.url {
            pickedFolder = url
            didImport = false
        }
    }

    private func runImport(folder: URL) async {
        let beforeCount = viewModel.totalCount
        await viewModel.importFolder(folder)
        importedCount = max(viewModel.totalCount - beforeCount, 0)
        if viewModel.error == nil {
            didImport = true
        }
    }

    /// Handle a folder dropped on the dropzone. Files are ignored — only
    /// directories trigger an import to mirror the picker's behavior and
    /// avoid surprising the user with single-file ingestion paths that don't
    /// match the step's framing.
    private func handleDrop(_ providers: [NSItemProvider]) -> Bool {
        guard let provider = providers.first else { return false }
        provider.loadItem(forTypeIdentifier: "public.file-url", options: nil) { item, _ in
            guard let data = item as? Data,
                  let url = URL(dataRepresentation: data, relativeTo: nil),
                  (try? url.resourceValues(forKeys: [.isDirectoryKey]).isDirectory) == true
            else { return }
            Task { @MainActor in
                self.pickedFolder = url
                self.didImport = false
            }
        }
        return true
    }
}
