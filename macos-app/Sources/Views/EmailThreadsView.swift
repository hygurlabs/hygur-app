import SwiftUI

struct EmailThreadsView: View {
    @State private var viewModel = EmailThreadsViewModel()
    @State private var selectedThread: EmailThread?
    @State private var searchText = ""
    @State private var syncStartedAt: Date? = nil

    private var filteredThreads: [EmailThread] {
        viewModel.threads.filter { thread in
            let matchesSearch = searchText.isEmpty
                || thread.subject.localizedCaseInsensitiveContains(searchText)
                || thread.participants.joined(separator: " ").localizedCaseInsensitiveContains(searchText)
            // Label filtering is enforced server-side when labels are selected
            // (loadThreads is re-triggered on label toggle). The client-side
            // pass-through keeps the view consistent while the fetch is in flight.
            return matchesSearch
        }
    }

    var body: some View {
        VStack(spacing: 0) {
            sourceSelector

            Divider()

            if viewModel.isSyncing {
                syncBanner
                    .transition(.move(edge: .top).combined(with: .opacity))
                Divider()
            }

            if !viewModel.labels.isEmpty {
                labelChips
                Divider()
            }

            if viewModel.isLoading {
                loadingState
            } else if viewModel.threads.isEmpty {
                emptyState
            } else {
                threadList
            }
        }
        .animation(.easeInOut(duration: 0.2), value: viewModel.isSyncing)
        .searchable(text: $searchText, prompt: "Search threads")
        .sheet(item: $selectedThread) { thread in
            ThreadDetailSheet(thread: thread, viewModel: viewModel)
        }
        .errorBannerOverlay(Binding(
            get: { viewModel.error },
            set: { _ in viewModel.clearError() }
        ))
        .task {
            await viewModel.loadAccounts()
            if viewModel.selectedAccountId != nil {
                await viewModel.loadThreads()
                await viewModel.loadLabels()
            }
        }
        .onChange(of: viewModel.isSyncing) { _, isSyncing in
            syncStartedAt = isSyncing ? Date() : nil
        }
    }

    // MARK: - Source Selector

    private var headerTitle: String {
        guard viewModel.selectedAccountId != nil else { return "Email Threads" }
        let total = viewModel.selectedAccountThreadCount
        if viewModel.selectedLabelIDs.isEmpty {
            return "Email Threads · \(total) mails"
        }
        let labelNames = viewModel.labels
            .filter { viewModel.selectedLabelIDs.contains($0.id) }
            .map(\.name)
            .joined(separator: ", ")
        return "Email Threads · \(total) mails · \(labelNames)"
    }

    // MARK: - Sync Banner

    private var syncBanner: some View {
        TimelineView(.periodic(from: syncStartedAt ?? Date(), by: 1.0)) { context in
            let elapsed = syncStartedAt.map { Int(context.date.timeIntervalSince($0)) } ?? 0
            HStack(spacing: HygurSpacing.sm) {
                LoadingIndicator(style: .small)

                Text(elapsed > 0
                     ? "Syncing · \(elapsed)s"
                     : "Syncing…")
                    .font(HygurTypography.caption)
                    .foregroundStyle(HygurColors.accent)

                Spacer()

                Text("Results will appear automatically")
                    .font(HygurTypography.caption)
                    .foregroundStyle(HygurColors.textSecondary)
            }
            .padding(.horizontal, HygurSpacing.lg)
            .padding(.vertical, HygurSpacing.sm)
            .background(HygurColors.accent.opacity(0.06))
        }
    }

    // MARK: - Label Chips

    private var labelChips: some View {
        ScrollView(.horizontal, showsIndicators: false) {
            HStack(spacing: HygurSpacing.sm) {
                ForEach(viewModel.labels) { label in
                    let isSelected = viewModel.selectedLabelIDs.contains(label.id)
                    Button {
                        toggleLabel(label.id)
                    } label: {
                        Text(label.name)
                            .font(HygurTypography.caption)
                            .padding(.horizontal, HygurSpacing.sm)
                            .padding(.vertical, HygurSpacing.xs)
                            .background(
                                isSelected ? HygurColors.accent : HygurColors.accent.opacity(0.12),
                                in: Capsule()
                            )
                            .foregroundStyle(
                                isSelected ? Color.white : HygurColors.accent
                            )
                    }
                    .buttonStyle(.plain)
                }

                if !viewModel.selectedLabelIDs.isEmpty {
                    Button {
                        clearLabels()
                    } label: {
                        Text("Clear all")
                            .font(HygurTypography.caption)
                            .foregroundStyle(HygurColors.textSecondary)
                    }
                    .buttonStyle(.plain)
                }
            }
            .padding(.horizontal, HygurSpacing.lg)
            .padding(.vertical, HygurSpacing.sm)
        }
    }

    private func toggleLabel(_ labelID: String) {
        if viewModel.selectedLabelIDs.contains(labelID) {
            viewModel.selectedLabelIDs.remove(labelID)
        } else {
            viewModel.selectedLabelIDs.insert(labelID)
        }
        Task { await viewModel.loadThreads() }
    }

    private func clearLabels() {
        viewModel.selectedLabelIDs = []
        Task { await viewModel.loadThreads() }
    }

    private var sourceSelector: some View {
        HStack(spacing: HygurSpacing.sm) {
            Text(headerTitle)
                .font(HygurTypography.headline)
                .foregroundStyle(HygurColors.textPrimary)

            Spacer()

            if viewModel.isLoading {
                LoadingIndicator(style: .small)
            }

            // Arrow next to the dropdown: triggers a full async sync of the
            // selected account (folders + labels + emails). Disabled while a
            // sync is in flight or before any account is selected.
            IconButton(systemImage: "arrow.clockwise", label: "Sync") {
                Task { await viewModel.triggerFullSync() }
            }
            .disabled(viewModel.isLoading || viewModel.isSyncing || viewModel.selectedAccountId == nil)

            Picker("Account", selection: Binding(
                get: { viewModel.selectedAccountId ?? "" },
                set: { newValue in
                    Task {
                        await viewModel.selectAccount(newValue.isEmpty ? nil : newValue)
                    }
                }
            )) {
                Text("Select an account…").tag("")
                ForEach(viewModel.accounts) { account in
                    HStack {
                        Circle()
                            .fill(BriefReason(rawValue: account.briefReason).isHealthy
                                  ? HygurColors.success
                                  : HygurColors.danger)
                            .frame(width: 8, height: 8)
                        Text(account.email)
                    }
                    .tag(account.accountId)
                }
            }
            .pickerStyle(.menu)
            .frame(minWidth: 200)
        }
        .padding(HygurSpacing.lg)
    }

    // MARK: - Loading State

    private var loadingState: some View {
        VStack(spacing: HygurSpacing.lg) {
            LoadingIndicator(style: .large)
            Text("Loading threads...")
                .font(HygurTypography.subheadline)
                .foregroundStyle(HygurColors.textSecondary)
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
    }

    // MARK: - Empty State

    @ViewBuilder
    private var emptyState: some View {
        if viewModel.selectedAccountId == nil {
            EmptyStateView(
                icon: "envelope.badge",
                title: "No account selected",
                subtitle: "Choose an account from the dropdown above."
            )
        } else if let account = viewModel.accounts.first(where: { $0.accountId == viewModel.selectedAccountId }), !account.isConnected {
            EmptyStateView(
                icon: "envelope.badge",
                title: "Account not connected",
                subtitle: account.briefReasonLocalized
            )
        } else {
            EmptyStateView(
                icon: "envelope.badge",
                title: "No mail",
                subtitle: "No threads synced for this account. Click the arrow to start a sync."
            )
        }
    }

    // MARK: - Thread List

    private var threadList: some View {
        List(selection: Binding(
            get: { selectedThread?.id },
            set: { newId in
                selectedThread = filteredThreads.first { $0.id == newId }
            }
        )) {
            ForEach(filteredThreads) { thread in
                EmailThreadRow(thread: thread)
                    .tag(thread.id)
                    .onTapGesture(count: 2) {
                        selectedThread = thread
                    }
            }
        }
        .listStyle(.inset)
    }
}

// MARK: - Email Thread Row

struct EmailThreadRow: View {
    let thread: EmailThread

    var body: some View {
        HStack {
            VStack(alignment: .leading, spacing: HygurSpacing.xs) {
                HStack {
                    Text(thread.subject)
                        .font(HygurTypography.body)
                        .fontWeight(.medium)
                        .lineLimit(1)

                    if thread.hasAttachments {
                        Image(systemName: "paperclip")
                            .font(HygurTypography.caption)
                            .foregroundStyle(HygurColors.textSecondary)
                            .accessibilityHidden(true)
                    }
                }

                HStack(spacing: HygurSpacing.sm) {
                    Text(participantsText)
                        .font(HygurTypography.caption)
                        .foregroundStyle(HygurColors.textSecondary)
                        .lineLimit(1)

                    BadgeView(
                        text: "\(thread.messageCount) messages",
                        color: HygurColors.accent,
                        style: .rounded
                    )
                }
            }

            Spacer()

            VStack(alignment: .trailing, spacing: HygurSpacing.xxs) {
                Text(formatDate(thread.dateEnd))
                    .font(HygurTypography.caption)
                    .foregroundStyle(HygurColors.textSecondary)

                if thread.dateStart != thread.dateEnd {
                    Text("from \(formatDate(thread.dateStart))")
                        .font(HygurTypography.caption)
                        .foregroundStyle(HygurColors.textTertiary)
                }
            }
        }
        .padding(.vertical, HygurSpacing.xs)
    }

    private var participantsText: String {
        thread.participants.prefix(3).joined(separator: ", ")
            + (thread.participants.count > 3 ? " +\(thread.participants.count - 3)" : "")
    }

    private func formatDate(_ dateString: String) -> String {
        let formatter = ISO8601DateFormatter()
        formatter.formatOptions = [.withInternetDateTime, .withFractionalSeconds]

        if let date = formatter.date(from: dateString) {
            let displayFormatter = DateFormatter()
            displayFormatter.dateStyle = .short
            displayFormatter.timeStyle = .none
            return displayFormatter.string(from: date)
        }

        formatter.formatOptions = [.withInternetDateTime]
        if let date = formatter.date(from: dateString) {
            let displayFormatter = DateFormatter()
            displayFormatter.dateStyle = .short
            displayFormatter.timeStyle = .none
            return displayFormatter.string(from: date)
        }

        return dateString
    }
}

// MARK: - Thread Detail Sheet

struct ThreadDetailSheet: View {
    let thread: EmailThread
    @Bindable var viewModel: EmailThreadsViewModel
    @State private var summary: EmailSummary?
    @State private var isSummarizing = false
    @State private var isIndexing = false
    @State private var indexSuccess = false
    @State private var selectedModel = "default"
    @Environment(\.dismiss) private var dismiss

    // Organization state
    @State private var tags: [Tag] = []
    @State private var availableTags: [Tag] = []
    @State private var projects: [Project] = []
    @State private var selectedProjectId: String?
    @State private var indexedContentId: String?
    @State private var isLoadingOrganization = false
    @State private var isSavingTag = false
    @State private var isSavingProject = false

    var body: some View {
        VStack(spacing: 0) {
            // Header
            HStack {
                VStack(alignment: .leading, spacing: HygurSpacing.xs) {
                    Text(thread.subject)
                        .font(.title2)
                        .fontWeight(.semibold)
                        .foregroundStyle(HygurColors.textPrimary)

                    Text(thread.participants.joined(separator: ", "))
                        .font(HygurTypography.subheadline)
                        .foregroundStyle(HygurColors.textSecondary)
                }

                Spacer()

                Button("Done") {
                    dismiss()
                }
                .keyboardShortcut(.escape)
            }
            .padding(HygurSpacing.lg)

            Divider()

            // Thread Info
            ScrollView {
                VStack(alignment: .leading, spacing: HygurSpacing.xl) {
                    // Metadata
                    GroupBox("Details") {
                        VStack(alignment: .leading, spacing: HygurSpacing.sm) {
                            DetailRow(label: "Messages", value: "\(thread.messageCount)")
                            DetailRow(label: "Attachments", value: thread.hasAttachments ? "Yes" : "No")
                            DetailRow(label: "Started", value: thread.dateStart)
                            DetailRow(label: "Last activity", value: thread.dateEnd)
                        }
                        .frame(maxWidth: .infinity, alignment: .leading)
                    }

                    // Actions
                    GroupBox("Actions") {
                        VStack(alignment: .leading, spacing: HygurSpacing.md) {
                            HStack {
                                Button {
                                    Task {
                                        isIndexing = true
                                        indexSuccess = await viewModel.indexThread(thread)
                                        isIndexing = false
                                        if indexSuccess {
                                            await loadOrganizationData()
                                        }
                                    }
                                } label: {
                                    HStack {
                                        if isIndexing {
                                            LoadingIndicator(style: .small)
                                        } else {
                                            Image(systemName: indexSuccess || indexedContentId != nil ? "checkmark.circle.fill" : "arrow.down.doc")
                                        }
                                        Text(indexSuccess || indexedContentId != nil ? "Indexed" : "Index to Knowledge Base")
                                    }
                                }
                                .disabled(isIndexing || indexSuccess || indexedContentId != nil)

                                Spacer()
                            }

                            Divider()

                            HStack {
                                TextField("Model", text: $selectedModel)
                                    .textFieldStyle(.roundedBorder)
                                    .frame(width: 150)

                                Button {
                                    Task {
                                        isSummarizing = true
                                        summary = await viewModel.summarizeThread(thread, model: selectedModel)
                                        isSummarizing = false
                                    }
                                } label: {
                                    HStack {
                                        if isSummarizing {
                                            LoadingIndicator(style: .small)
                                        } else {
                                            Image(systemName: "text.badge.star")
                                        }
                                        Text("Summarize")
                                    }
                                }
                                .disabled(isSummarizing)

                                Spacer()
                            }
                        }
                        .frame(maxWidth: .infinity, alignment: .leading)
                    }

                    // Summary (if available)
                    if let summary = summary {
                        GroupBox("Summary") {
                            VStack(alignment: .leading, spacing: HygurSpacing.lg) {
                                if !summary.decisions.isEmpty {
                                    SummarySection(title: "Decisions", icon: "checkmark.seal", items: summary.decisions)
                                }

                                if !summary.actions.isEmpty {
                                    SummarySection(title: "Actions", icon: "arrow.right.circle", items: summary.actions)
                                }

                                if !summary.openQuestions.isEmpty {
                                    SummarySection(title: "Open Questions", icon: "questionmark.circle", items: summary.openQuestions)
                                }
                            }
                            .frame(maxWidth: .infinity, alignment: .leading)
                        }
                    }

                    // Organization (only shown after indexing)
                    if indexedContentId != nil {
                        organizationSection
                    }
                }
                .padding(HygurSpacing.lg)
            }
        }
        .frame(minWidth: 500, minHeight: 400)
        .task {
            await loadOrganizationData()
        }
    }

    // MARK: - Organization Section

    @ViewBuilder
    private var organizationSection: some View {
        GroupBox("Organization") {
            VStack(alignment: .leading, spacing: HygurSpacing.lg) {
                tagsSection

                Divider()

                projectSection
            }
            .frame(maxWidth: .infinity, alignment: .leading)
        }
    }

    @ViewBuilder
    private var tagsSection: some View {
        VStack(alignment: .leading, spacing: HygurSpacing.sm) {
            HStack {
                Text("Tags")
                    .font(HygurTypography.subheadline)
                    .foregroundStyle(HygurColors.textSecondary)
                Spacer()
                if isSavingTag {
                    LoadingIndicator(style: .small)
                }
                if !tags.isEmpty {
                    Button("Clear all") {
                        Task {
                            await removeAllTags()
                        }
                    }
                    .font(HygurTypography.caption)
                    .buttonStyle(.plain)
                    .foregroundStyle(HygurColors.textSecondary)
                    .disabled(isSavingTag)
                }
            }

            if isLoadingOrganization {
                HStack(spacing: HygurSpacing.xs + 2) {
                    LoadingIndicator(style: .small)
                    Text("Loading tags...")
                        .font(HygurTypography.caption)
                        .foregroundStyle(HygurColors.textSecondary)
                }
            } else if availableTags.isEmpty {
                Text("No tags available. Create tags in the Tags section.")
                    .font(HygurTypography.caption)
                    .foregroundStyle(HygurColors.textTertiary)
            } else {
                // Selected tags
                if !tags.isEmpty {
                    FlowLayout(spacing: HygurSpacing.sm) {
                        ForEach(tags) { tag in
                            TagPillView(tag: tag, showRemoveButton: true) {
                                Task {
                                    await removeTag(tag)
                                }
                            }
                        }
                    }
                    .padding(.bottom, HygurSpacing.xs)
                }

                // Available tags (not yet selected)
                let unselectedTags = availableTags.filter { availableTag in
                    !tags.contains { $0.id == availableTag.id }
                }
                if !unselectedTags.isEmpty {
                    FlowLayout(spacing: HygurSpacing.sm) {
                        ForEach(unselectedTags) { tag in
                            SelectableTagPillView(tag: tag, isSelected: false) {
                                Task {
                                    await addTag(tag)
                                }
                            }
                        }
                    }
                }
            }
        }
    }

    @ViewBuilder
    private var projectSection: some View {
        VStack(alignment: .leading, spacing: HygurSpacing.sm) {
            HStack {
                Text("Project")
                    .font(HygurTypography.subheadline)
                    .foregroundStyle(HygurColors.textSecondary)
                Spacer()
                if isSavingProject {
                    LoadingIndicator(style: .small)
                }
            }

            if isLoadingOrganization {
                HStack(spacing: HygurSpacing.xs + 2) {
                    LoadingIndicator(style: .small)
                    Text("Loading projects...")
                        .font(HygurTypography.caption)
                        .foregroundStyle(HygurColors.textSecondary)
                }
            } else {
                Picker("Project", selection: Binding(
                    get: { selectedProjectId },
                    set: { newValue in
                        Task {
                            await updateProject(newValue)
                        }
                    }
                )) {
                    Text("None")
                        .tag(String?.none)
                    ForEach(projects) { project in
                        Text(project.name)
                            .tag(String?.some(project.id))
                    }
                }
                .labelsHidden()
                .pickerStyle(.menu)
                .disabled(isSavingProject)
            }
        }
    }

    // MARK: - Organization Actions

    private func loadOrganizationData() async {
        isLoadingOrganization = true
        defer { isLoadingOrganization = false }

        // Knowledge items are keyed by account_id under the multi-account
        // schema; fall back to provider name only if no account is selected
        // (legacy data) so the lookup still works during migration.
        guard let key = viewModel.selectedAccountId ?? viewModel.selectedSourceLegacy else { return }
        let contentId = "mail:\(key):\(thread.id)"

        do {
            if let item = try await viewModel.sidecarService.getKnowledgeItem(contentId: contentId) {
                indexedContentId = item.contentId
                tags = item.tags
                selectedProjectId = item.projectId
            }

            async let tagsResult = viewModel.sidecarService.listTags()
            async let projectsResult = viewModel.sidecarService.listProjects()

            availableTags = try await tagsResult
            projects = try await projectsResult.filter { !$0.archived }
        } catch {
            print("Failed to load organization data: \(error)")
        }
    }

    private func addTag(_ tag: Tag) async {
        guard let contentId = indexedContentId else { return }
        isSavingTag = true
        defer { isSavingTag = false }

        do {
            let updatedItem = try await viewModel.sidecarService.addTagToItem(contentId: contentId, tagId: tag.id)
            tags = updatedItem.tags
        } catch {
            print("Failed to add tag: \(error)")
        }
    }

    private func removeTag(_ tag: Tag) async {
        guard let contentId = indexedContentId else { return }
        isSavingTag = true
        defer { isSavingTag = false }

        do {
            let updatedItem = try await viewModel.sidecarService.removeTagFromItem(contentId: contentId, tagId: tag.id)
            tags = updatedItem.tags
        } catch {
            print("Failed to remove tag: \(error)")
        }
    }

    private func removeAllTags() async {
        guard let contentId = indexedContentId else { return }
        isSavingTag = true
        defer { isSavingTag = false }

        for tag in tags {
            do {
                let updatedItem = try await viewModel.sidecarService.removeTagFromItem(contentId: contentId, tagId: tag.id)
                tags = updatedItem.tags
            } catch {
                print("Failed to remove tag \(tag.name): \(error)")
            }
        }
    }

    private func updateProject(_ newProjectId: String?) async {
        guard let contentId = indexedContentId else { return }

        if newProjectId == selectedProjectId { return }

        isSavingProject = true
        defer { isSavingProject = false }

        do {
            if let projectId = newProjectId {
                let updatedItem = try await viewModel.sidecarService.linkItemToProject(contentId: contentId, projectId: projectId)
                selectedProjectId = updatedItem.projectId
            } else {
                let updatedItem = try await viewModel.sidecarService.unlinkItemFromProject(contentId: contentId)
                selectedProjectId = updatedItem.projectId
            }
        } catch {
            print("Failed to update project: \(error)")
        }
    }
}

// MARK: - Helper Views

struct DetailRow: View {
    let label: String
    let value: String

    var body: some View {
        HStack {
            Text(label)
                .foregroundStyle(HygurColors.textSecondary)
            Spacer()
            Text(value)
                .foregroundStyle(HygurColors.textPrimary)
        }
    }
}

struct SummarySection: View {
    let title: String
    let icon: String
    let items: [String]

    var body: some View {
        VStack(alignment: .leading, spacing: HygurSpacing.sm) {
            Label(title, systemImage: icon)
                .font(HygurTypography.subheadline)
                .fontWeight(.medium)
                .foregroundStyle(HygurColors.textPrimary)

            ForEach(items, id: \.self) { item in
                HStack(alignment: .top, spacing: HygurSpacing.sm) {
                    Text("-")
                        .foregroundStyle(HygurColors.textSecondary)
                    Text(item)
                        .foregroundStyle(HygurColors.textPrimary)
                }
                .font(HygurTypography.body)
            }
        }
    }
}

// MARK: - Preview

#Preview {
    EmailThreadsView()
        .frame(width: 600, height: 400)
}
