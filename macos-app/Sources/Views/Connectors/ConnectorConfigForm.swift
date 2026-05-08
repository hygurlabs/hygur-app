import SwiftUI
import UniformTypeIdentifiers
import AppKit

struct ConnectorConfigForm: View {
    let schema: ConnectorConfigSchema
    let config: ConnectorConfig
    let connectorId: String
    let viewModel: ConnectorsViewModel
    let onSave: ([String: String]) async -> Void

    @State private var fieldValues: [String: String] = [:]
    @State private var isSaving = false
    @State private var showSecrets: Set<String> = []
    @State private var showingFilePicker = false
    @State private var activeFilePickerKey: String?
    @State private var oauthLoading: Set<String> = []
    @State private var showOAuthCodeEntry = false
    @State private var oauthCodeInput = ""
    @State private var pendingOAuthField: ConnectorConfigField?
    @State private var mailboxOptions: [String] = []
    @State private var labelOptions: [MailboxOption] = []
    @State private var isFetchingMailboxes = false
    @State private var isFetchingLabels = false
    @State private var providerValue = ""

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            ForEach(schema.groups, id: \.title) { group in
                groupSection(group)
            }

            saveButton
        }
        .onAppear {
            populateInitialValues()
            fetchDynamicOptions()
        }
        .fileImporter(
            isPresented: $showingFilePicker,
            allowedContentTypes: [.folder, .item],
            allowsMultipleSelection: false
        ) { result in
            handleFilePicked(result)
        }
        .sheet(isPresented: $showOAuthCodeEntry) {
            oauthCodeEntrySheet
        }
    }

    // MARK: - Group section

    private func groupSection(_ group: ConnectorConfigGroup) -> some View {
        VStack(alignment: .leading, spacing: 10) {
            Text(group.title)
                .font(.subheadline)
                .fontWeight(.semibold)
                .foregroundStyle(.secondary)
                .padding(.top, 4)

            VStack(spacing: 8) {
                ForEach(group.fields, id: \.key) { field in
                    if conditionMet(field) {
                        fieldRow(field)
                            .transition(.opacity)
                    } else {
                        fieldRow(field)
                            .opacity(0)
                            .frame(height: 0)
                            .clipped()
                    }
                }
            }
            .padding(.bottom, 8)
        }
    }

    // MARK: - Field rendering dispatcher

    @ViewBuilder
    private func fieldRow(_ field: ConnectorConfigField) -> some View {
        VStack(alignment: .leading, spacing: 4) {
            HStack(spacing: 4) {
                Text(field.label)
                    .font(.subheadline)
                if field.required {
                    Text("*")
                        .font(.caption)
                        .foregroundStyle(.red)
                }
            }

            switch field.fieldType {
            case "string":
                stringField(field)
            case "secret":
                secretField(field)
            case "enum":
                enumField(field)
            case "multi_enum":
                multiEnumField(field)
            case "oauth":
                oauthField(field)
            case "path":
                pathField(field)
            case "cron":
                cronField(field)
            case "bool":
                boolField(field)
            case "int":
                intField(field)
            case "permission_check":
                permissionCheckField(field)
            default:
                stringField(field)
            }

            if !field.description.isEmpty {
                Text(field.description)
                    .font(.caption)
                    .foregroundStyle(.tertiary)
            }
        }
    }

    // MARK: - String field

    private func stringField(_ field: ConnectorConfigField) -> some View {
        TextField(
            field.label,
            text: Binding(
                get: { fieldValues[field.key] ?? "" },
                set: { fieldValues[field.key] = $0 }
            )
        )
        .textFieldStyle(.roundedBorder)
    }

    // MARK: - Secret field

    private func secretField(_ field: ConnectorConfigField) -> some View {
        HStack(spacing: 6) {
            let isVisible = showSecrets.contains(field.key)

            if isVisible {
                TextField(
                    field.label,
                    text: Binding(
                        get: { fieldValues[field.key] ?? "" },
                        set: { fieldValues[field.key] = $0 }
                    )
                )
                .textFieldStyle(.roundedBorder)
            } else {
                SecureField(
                    field.label,
                    text: Binding(
                        get: { fieldValues[field.key] ?? "" },
                        set: { fieldValues[field.key] = $0 }
                    )
                )
                .textFieldStyle(.roundedBorder)
            }

            Button {
                if showSecrets.contains(field.key) {
                    showSecrets.remove(field.key)
                } else {
                    showSecrets.insert(field.key)
                }
            } label: {
                Image(systemName: showSecrets.contains(field.key) ? "eye.slash" : "eye")
                    .foregroundStyle(.secondary)
            }
            .buttonStyle(.plain)
        }
    }

    // MARK: - Enum field (Picker)

    private func enumField(_ field: ConnectorConfigField) -> some View {
        Group {
            if field.options.isEmpty {
                Text("No options available")
                    .font(.caption)
                    .foregroundStyle(.orange)
            } else {
                let binding = Binding(
                    get: { fieldValues[field.key] ?? field.default },
                    set: { fieldValues[field.key] = $0 }
                )
                Picker(field.label, selection: binding) {
                    ForEach(field.options, id: \.value) { option in
                        Text(option.label).tag(option.value)
                    }
                }
                .pickerStyle(.menu)
            }
        }
    }

    // MARK: - Multi-enum field (Checkboxes)

    private func multiEnumField(_ field: ConnectorConfigField) -> some View {
        let selectedValues = Set((fieldValues[field.key] ?? "").split(separator: ",").map(String.init))
        let options = field.options.isEmpty ? getDynamicOptions(field) : field.options

        return VStack(alignment: .leading, spacing: 4) {
            if options.isEmpty {
                if isFetchingMailboxes || isFetchingLabels {
                    ProgressView()
                        .font(.caption)
                } else {
                    Text("No options available")
                        .font(.caption)
                        .foregroundStyle(.orange)
                }
            } else {
                ForEach(options, id: \.value) { option in
                    Toggle(isOn: Binding(
                        get: { selectedValues.contains(option.value) },
                        set: { isOn in
                            var newSelected = selectedValues
                            if isOn {
                                newSelected.insert(option.value)
                            } else {
                                newSelected.remove(option.value)
                            }
                            fieldValues[field.key] = newSelected.joined(separator: ",")
                        }
                    )) {
                        Text(option.label)
                    }
                    .toggleStyle(.checkbox)
                }
            }
        }
    }

    private func getDynamicOptions(_ field: ConnectorConfigField) -> [ConnectorConfigOption] {
        if field.key == "gmail_mailbox" {
            return labelOptions.map { .init(value: $0.id, label: "\($0.name) (\($0.type))", icon: nil) }
        }
        if field.key == "proton_mailbox" {
            return mailboxOptions.map { .init(value: $0, label: $0, icon: nil) }
        }
        return []
    }

    // MARK: - OAuth field

    private func oauthField(_ field: ConnectorConfigField) -> some View {
        HStack {
            Button {
                startOAuth(field: field)
            } label: {
                HStack(spacing: 6) {
                    if oauthLoading.contains(field.key) {
                        ProgressView()
                            .controlSize(.small)
                    } else {
                        Image(systemName: "link.badge.plus")
                    }
                    Text("Connect with OAuth")
                }
            }
            .buttonStyle(.borderedProminent)
            .disabled(oauthLoading.contains(field.key))

            if let val = fieldValues[field.key], !val.isEmpty {
                Image(systemName: "checkmark.circle.fill")
                    .foregroundStyle(.green)
                    .accessibilityHidden(true)
                Text("Connected")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }
        }
    }

    // MARK: - Path field (supports multiple folders, one per line)

    private func pathField(_ field: ConnectorConfigField) -> some View {
        let paths = parsePathList(fieldValues[field.key])
        return VStack(alignment: .leading, spacing: HygurSpacing.xs) {
            ForEach(Array(paths.enumerated()), id: \.offset) { index, path in
                HStack(spacing: HygurSpacing.xs) {
                    Image(systemName: "folder")
                        .foregroundStyle(HygurColors.textSecondary)
                        .accessibilityHidden(true)
                    Text(path)
                        .font(HygurTypography.body)
                        .lineLimit(1)
                        .truncationMode(.middle)
                        .frame(maxWidth: .infinity, alignment: .leading)
                    IconButton(systemImage: "xmark.circle.fill", label: "Remove folder") {
                        var current = paths
                        current.remove(at: index)
                        fieldValues[field.key] = formatPathList(current)
                    }
                }
                .padding(.horizontal, HygurSpacing.sm)
                .padding(.vertical, HygurSpacing.xs)
                .background(HygurColors.surface)
                .clipShape(RoundedRectangle(cornerRadius: HygurRadius.sm))
            }

            Button {
                activeFilePickerKey = field.key
                showingFilePicker = true
            } label: {
                Label(paths.isEmpty ? "Add folder" : "Add another folder", systemImage: "folder.badge.plus")
                    .font(HygurTypography.caption)
            }
            .buttonStyle(.bordered)
            .controlSize(.small)
        }
    }

    private func parsePathList(_ raw: String?) -> [String] {
        guard let raw, !raw.isEmpty else { return [] }
        return raw
            .components(separatedBy: .newlines)
            .map { $0.trimmingCharacters(in: .whitespaces) }
            .filter { !$0.isEmpty }
    }

    private func formatPathList(_ paths: [String]) -> String {
        paths.joined(separator: "\n")
    }

    // MARK: - Bool field (Toggle)

    private func boolField(_ field: ConnectorConfigField) -> some View {
        Toggle(isOn: Binding(
            get: { (fieldValues[field.key] ?? field.default) == "true" },
            set: { fieldValues[field.key] = $0 ? "true" : "false" }
        )) {
            EmptyView()
        }
        .toggleStyle(.switch)
        .labelsHidden()
        .frame(maxWidth: .infinity, alignment: .leading)
    }

    // MARK: - Int field

    private func intField(_ field: ConnectorConfigField) -> some View {
        TextField(
            field.label,
            text: Binding(
                get: { fieldValues[field.key] ?? field.default },
                set: { newValue in
                    let filtered = newValue.filter { $0.isNumber }
                    fieldValues[field.key] = filtered
                }
            )
        )
        .textFieldStyle(.roundedBorder)
    }

    // MARK: - Permission check field

    /// Renders an info card with a button that opens a System Settings pane.
    /// Stores no value — it is purely informational. The button label comes
    /// from `field.default`; the destination URL is resolved by `field.key`.
    private func permissionCheckField(_ field: ConnectorConfigField) -> some View {
        let buttonLabel = field.default.isEmpty ? "Open System Settings" : field.default
        let url = permissionCheckURL(forKey: field.key)
        return Button {
            if let url { NSWorkspace.shared.open(url) }
        } label: {
            HStack(spacing: 6) {
                Image(systemName: "lock.shield")
                Text(buttonLabel)
            }
        }
        .buttonStyle(.borderedProminent)
        .disabled(url == nil)
    }

    /// Maps a permission-check field key to the System Settings URL it should
    /// open. Returns nil for unknown keys, which disables the button.
    private func permissionCheckURL(forKey key: String) -> URL? {
        switch key {
        case "mailapp_automation":
            return URL(string: "x-apple.systempreferences:com.apple.preference.security?Privacy_Automation")
        default:
            return nil
        }
    }

    // MARK: - Cron field

    private func cronField(_ field: ConnectorConfigField) -> some View {
        VStack(alignment: .leading, spacing: 6) {
            HStack(spacing: 8) {
                cronPresetButton(label: "1h", cron: "0 * * * *", key: field.key)
                cronPresetButton(label: "6h", cron: "0 */6 * * *", key: field.key)
                cronPresetButton(label: "24h", cron: "0 0 * * *", key: field.key)
            }

            TextField(
                "Cron expression",
                text: Binding(
                    get: { fieldValues[field.key] ?? "" },
                    set: { fieldValues[field.key] = $0 }
                )
            )
            .textFieldStyle(.roundedBorder)
            .font(.system(.body, design: .monospaced))
        }
    }

    private func cronPresetButton(label: String, cron: String, key: String) -> some View {
        let isSelected = fieldValues[key] == cron
        return Button {
            fieldValues[key] = cron
        } label: {
            Text(label)
                .font(.caption)
                .fontWeight(.medium)
                .padding(.horizontal, 10)
                .padding(.vertical, 4)
        }
        .buttonStyle(.bordered)
        .tint(isSelected ? .accentColor : .secondary)
    }

    // MARK: - Save button

    private var saveButton: some View {
        HStack {
            Spacer()
            Button {
                Task {
                    isSaving = true
                    await onSave(fieldValues)
                    isSaving = false
                }
            } label: {
                if isSaving {
                    HStack(spacing: 6) {
                        ProgressView().controlSize(.small)
                        Text("Saving...")
                    }
                } else {
                    Text("Save")
                        .fontWeight(.medium)
                }
            }
            .buttonStyle(.borderedProminent)
            .disabled(isSaving)
            .padding(.top, 8)
        }
    }

    // MARK: - Condition evaluation

    func conditionMet(_ field: ConnectorConfigField) -> Bool {
        guard let condition = field.condition else { return true }
        let currentValue = fieldValues[condition.field] ?? ""
        return currentValue == condition.value
    }

    // MARK: - Initial population

    private func populateInitialValues() {
        var values: [String: String] = [:]

        for group in schema.groups {
            for field in group.fields {
                values[field.key] = field.default
            }
        }

        for (key, value) in config.settings {
            values[key] = value
        }

        let keychainSecrets = KeychainService.loadSecrets(connectorId: connectorId, schema: schema)
        for (key, value) in keychainSecrets {
            if values[key] == nil || values[key]?.isEmpty == true {
                values[key] = value
            }
        }

        fieldValues = values
    }

    // MARK: - OAuth handler

    private func startOAuth(field: ConnectorConfigField) {
        oauthLoading.insert(field.key)
        Task {
            do {
                // Save current form state first so the sidecar re-initialises
                // with the selected provider before generating the auth URL.
                await onSave(fieldValues)
                let urlString = try await viewModel.service.getConnectorAuthURL(connectorId)
                guard let url = URL(string: urlString), !urlString.isEmpty else {
                    oauthLoading.remove(field.key)
                    viewModel.error = "OAuth error: the server returned an empty URL. Check that your provider is configured correctly."
                    return
                }
                NSWorkspace.shared.open(url)
                // Keep the spinner active and ask the user to paste the code
                // that Google displays in the browser after authorisation.
                pendingOAuthField = field
                showOAuthCodeEntry = true
            } catch {
                oauthLoading.remove(field.key)
                viewModel.error = "OAuth error: \(error.localizedDescription)"
            }
        }
    }

    private func submitOAuthCode() {
        guard let field = pendingOAuthField else { return }
        let code = oauthCodeInput.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !code.isEmpty else { return }
        showOAuthCodeEntry = false
        oauthCodeInput = ""
        pendingOAuthField = nil
        Task {
            defer { oauthLoading.remove(field.key) }
            do {
                try await viewModel.service.authConnectorCallback(connectorId, code: code)
                fieldValues[field.key] = "connected"
                try KeychainService.save(connectorId: connectorId, key: field.key, value: "connected")
                await onSave(fieldValues)
                Task { try? await viewModel.sync(id: connectorId) }
            } catch {
                viewModel.error = "OAuth error: \(error.localizedDescription)"
            }
        }
    }

    private var oauthCodeEntrySheet: some View {
        VStack(alignment: .leading, spacing: 16) {
            Text("Google authorization")
                .font(.headline)

            Text("Google displayed an authorization code in your browser. Copy it and paste it below.")
                .font(.subheadline)
                .foregroundStyle(.secondary)

            TextField("Authorization code", text: $oauthCodeInput)
                .textFieldStyle(.roundedBorder)

            HStack {
                Button("Cancel") {
                    showOAuthCodeEntry = false
                    oauthCodeInput = ""
                    if let field = pendingOAuthField {
                        oauthLoading.remove(field.key)
                    }
                    pendingOAuthField = nil
                }
                .keyboardShortcut(.cancelAction)

                Spacer()

                Button("Connect") {
                    submitOAuthCode()
                }
                .keyboardShortcut(.defaultAction)
                .disabled(oauthCodeInput.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)
                .buttonStyle(.borderedProminent)
            }
        }
        .padding(.horizontal, 24)
    }

    // MARK: - Dynamic options fetching

    private func fetchDynamicOptions() {
        Task {
            // Only fetch for the mail connector
            if connectorId == "mail" {
                isFetchingMailboxes = true
                isFetchingLabels = true
                do {
                    let mailboxes = try await viewModel.service.fetchMailboxes(connectorId: connectorId)
                    mailboxOptions = mailboxes
                } catch {
                    print("Failed to fetch mailboxes: \(error)")
                    mailboxOptions = []
                }
                do {
                    let labels = try await viewModel.service.fetchLabels(connectorId: connectorId)
                    labelOptions = labels
                } catch {
                    print("Failed to fetch labels: \(error)")
                    labelOptions = []
                }
                isFetchingMailboxes = false
                isFetchingLabels = false
            }
        }
    }

    // MARK: - OAuth handler

    private func handleFilePicked(_ result: Result<[URL], Error>) {
        guard let key = activeFilePickerKey else { return }
        activeFilePickerKey = nil
        switch result {
        case .success(let urls):
            guard let url = urls.first else { return }
            if isPathField(key) {
                var existing = parsePathList(fieldValues[key])
                if !existing.contains(url.path) {
                    existing.append(url.path)
                }
                fieldValues[key] = formatPathList(existing)
            } else {
                fieldValues[key] = url.path
            }
        case .failure:
            break
        }
    }

    private func isPathField(_ key: String) -> Bool {
        for group in schema.groups {
            for field in group.fields where field.key == key {
                return field.fieldType == "path"
            }
        }
        return false
    }
}
