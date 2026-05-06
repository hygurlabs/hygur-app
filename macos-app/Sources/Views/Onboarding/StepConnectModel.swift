import SwiftUI

/// Step 2 of the onboarding flow — point Hygur at a local OpenAI-compatible
/// LLM (LM Studio, Ollama, vLLM, llama.cpp…). Two modes:
///
///   • Simple    — single URL field + "Auto-detect" scan of common ports
///                 (1234, 8080, 11434). After detection, surfaces the model
///                 list so the user can pick one.
///   • Advanced  — exposes the bearer token and a separate embeddings URL /
///                 model, mirroring the LMStudioTab in Settings.
///
/// "Test & continue" issues a real `/v1/chat/completions` round-trip with a
/// short prompt before persisting; on success it patches the sidecar config
/// (LM Studio URL, models, embedding URL) and restarts the supervisor so the
/// new endpoint takes effect immediately.
struct StepConnectModel: View {
    let onAdvance: () -> Void

    @Environment(SidecarSupervisor.self) private var supervisor

    @State private var mode: Mode = .simple
    @State private var url: String = "http://localhost:1234"
    @State private var token: String = ""
    @State private var embeddingURL: String = ""
    @State private var modelDefault: String = ""
    @State private var embeddingModel: String = ""
    @State private var inferenceModelOptions: [String] = []
    @State private var embeddingModelOptions: [String] = []
    @State private var status: Status = .idle

    private let sidecar = SidecarService.fromSettings()

    /// Common loopback ports for local OpenAI-compat runtimes:
    /// LM Studio, llama.cpp / vLLM, Ollama (in that order).
    private static let detectionPorts: [Int] = [1234, 8080, 11434]

    private enum Mode: String, CaseIterable, Identifiable {
        case simple, advanced
        var id: String { rawValue }
        var label: String {
            switch self {
            case .simple:   return "Simple"
            case .advanced: return "Advanced"
            }
        }
    }

    private enum Status: Equatable {
        case idle
        case detecting
        case fetchingModels
        case testing
        case saving
        case restarting
        case success
        case error(String)

        var isBusy: Bool {
            switch self {
            case .detecting, .fetchingModels, .testing, .saving, .restarting:
                return true
            default:
                return false
            }
        }
    }

    var body: some View {
        VStack(spacing: HygurSpacing.lg) {
            header

            ScrollView {
                VStack(spacing: HygurSpacing.lg) {
                    modePicker

                    Group {
                        if mode == .simple { simpleForm } else { advancedForm }
                    }

                    statusBanner
                }
                .padding(.horizontal, HygurSpacing.xxxl)
                .padding(.bottom, HygurSpacing.lg)
                .frame(maxWidth: 600)
                .frame(maxWidth: .infinity)
            }

            primaryAction
                .padding(.bottom, HygurSpacing.lg)
        }
        .padding(.top, HygurSpacing.xl)
        .frame(maxWidth: .infinity, maxHeight: .infinity)
        .task {
            await loadExistingConfig()
        }
    }

    // MARK: - Layout

    private var header: some View {
        VStack(spacing: HygurSpacing.sm) {
            Image(systemName: "cpu")
                .font(.system(size: 36, weight: .light))
                .foregroundStyle(HygurColors.accent)
            Text("Connect AI model")
                .font(HygurTypography.title)
                .foregroundStyle(HygurColors.textPrimary)
            Text("Point Hygur to your local AI runtime. LM Studio, Ollama and vLLM are detected automatically.")
                .font(HygurTypography.body)
                .foregroundStyle(HygurColors.textSecondary)
                .multilineTextAlignment(.center)
                .frame(maxWidth: 460)
        }
    }

    private var modePicker: some View {
        Picker("", selection: $mode) {
            ForEach(Mode.allCases) { m in
                Text(m.label).tag(m)
            }
        }
        .pickerStyle(.segmented)
        .frame(maxWidth: 240)
    }

    // Simple — a single URL + autodetect + a model picker.
    private var simpleForm: some View {
        VStack(alignment: .leading, spacing: HygurSpacing.md) {
            fieldLabel("API URL")
            HStack(spacing: HygurSpacing.sm) {
                TextField("http://localhost:1234", text: $url)
                    .textFieldStyle(.roundedBorder)
                    .disableAutocorrection(true)
                    .onChange(of: url) { _, _ in
                        inferenceModelOptions = []
                        if status != .idle, !status.isBusy {
                            status = .idle
                        }
                    }
                Button {
                    Task { await autoDetect() }
                } label: {
                    if status == .detecting {
                        ProgressView().controlSize(.small)
                    } else {
                        Text("Auto-detect")
                    }
                }
                .disabled(status.isBusy)
            }

            fieldHint("Scans the common ports (1234, 8080, 11434) on this Mac.")

            modelPicker(
                title: "Model",
                selection: $modelDefault,
                options: inferenceModelOptions,
                isLoading: status == .fetchingModels,
                onRefresh: { Task { await refreshInferenceModels(force: true) } }
            )
        }
    }

    // Advanced — exposes token + a dedicated embeddings endpoint/model.
    private var advancedForm: some View {
        VStack(alignment: .leading, spacing: HygurSpacing.lg) {
            VStack(alignment: .leading, spacing: HygurSpacing.md) {
                fieldLabel("Inference URL")
                TextField("http://localhost:1234", text: $url)
                    .textFieldStyle(.roundedBorder)
                    .disableAutocorrection(true)
                    .onChange(of: url) { _, _ in
                        inferenceModelOptions = []
                    }

                fieldLabel("API token")
                SecureField("Optional bearer token", text: $token)
                    .textFieldStyle(.roundedBorder)

                modelPicker(
                    title: "Inference model",
                    selection: $modelDefault,
                    options: inferenceModelOptions,
                    isLoading: status == .fetchingModels,
                    onRefresh: { Task { await refreshInferenceModels(force: true) } }
                )
            }

            Divider()

            VStack(alignment: .leading, spacing: HygurSpacing.md) {
                fieldLabel("Embeddings URL (optional)")
                TextField("Leave empty to reuse the inference URL", text: $embeddingURL)
                    .textFieldStyle(.roundedBorder)
                    .disableAutocorrection(true)
                    .onChange(of: embeddingURL) { _, _ in
                        embeddingModelOptions = []
                    }

                modelPicker(
                    title: "Embedding model",
                    selection: $embeddingModel,
                    options: embeddingModelOptions,
                    isLoading: false,
                    onRefresh: { Task { await refreshEmbeddingModels(force: true) } }
                )
            }
        }
    }

    @ViewBuilder
    private var statusBanner: some View {
        switch status {
        case .idle, .detecting, .fetchingModels:
            EmptyView()
        case .testing:
            statusRow(icon: "paperplane", tint: HygurColors.accent, text: "Sending a test prompt…")
        case .saving:
            statusRow(icon: "tray.and.arrow.down", tint: HygurColors.accent, text: "Saving configuration…")
        case .restarting:
            statusRow(icon: "arrow.triangle.2.circlepath", tint: HygurColors.accent, text: "Restarting sidecar…")
        case .success:
            statusRow(icon: "checkmark.seal.fill", tint: HygurColors.success, text: "Connected. Continuing…")
        case .error(let message):
            statusRow(icon: "exclamationmark.triangle.fill", tint: HygurColors.danger, text: message)
        }
    }

    private var primaryAction: some View {
        Button(action: { Task { await testAndContinue() } }) {
            HStack(spacing: HygurSpacing.sm) {
                if status.isBusy {
                    ProgressView().controlSize(.small)
                }
                Text(status.isBusy ? "Connecting…" : "Test & continue")
                    .frame(minWidth: 140)
            }
        }
        .buttonStyle(.borderedProminent)
        .controlSize(.large)
        .keyboardShortcut(.defaultAction)
        .tint(HygurColors.accent)
        .disabled(status.isBusy || url.trimmingCharacters(in: .whitespaces).isEmpty || modelDefault.trimmingCharacters(in: .whitespaces).isEmpty)
    }

    // MARK: - Field helpers

    private func fieldLabel(_ text: String) -> some View {
        Text(text)
            .font(HygurTypography.subheadline.weight(.medium))
            .foregroundStyle(HygurColors.textPrimary)
    }

    private func fieldHint(_ text: String) -> some View {
        Text(text)
            .font(HygurTypography.caption)
            .foregroundStyle(HygurColors.textSecondary)
    }

    private func statusRow(icon: String, tint: Color, text: String) -> some View {
        HStack(spacing: HygurSpacing.sm) {
            Image(systemName: icon).foregroundStyle(tint)
            Text(text)
                .font(HygurTypography.caption)
                .foregroundStyle(HygurColors.textSecondary)
            Spacer()
        }
        .padding(HygurSpacing.md)
        .background(
            RoundedRectangle(cornerRadius: 8, style: .continuous)
                .fill(HygurColors.surface)
        )
    }

    /// A free-form text field paired with a dropdown of detected model IDs.
    /// Lets the user either type a model name or pick from what `/v1/models`
    /// returned — useful when the server reports a single loaded model
    /// (LM Studio) or a curated list (Ollama, vLLM).
    private func modelPicker(
        title: String,
        selection: Binding<String>,
        options: [String],
        isLoading: Bool,
        onRefresh: @escaping () -> Void
    ) -> some View {
        VStack(alignment: .leading, spacing: HygurSpacing.xs) {
            fieldLabel(title)
            HStack(spacing: HygurSpacing.sm) {
                TextField("e.g. llama-3.1-8b-instruct", text: selection)
                    .textFieldStyle(.roundedBorder)
                    .disableAutocorrection(true)

                if isLoading {
                    ProgressView().controlSize(.small)
                } else if !options.isEmpty {
                    Menu {
                        ForEach(options, id: \.self) { option in
                            Button(option) { selection.wrappedValue = option }
                        }
                    } label: {
                        Image(systemName: "list.bullet")
                    }
                    .menuStyle(.borderlessButton)
                    .fixedSize()
                } else {
                    Button(action: onRefresh) {
                        Image(systemName: "arrow.clockwise")
                    }
                    .buttonStyle(.borderless)
                }
            }
        }
    }

    // MARK: - Sidecar config IO

    /// Hydrate the form from the sidecar's current config. The user may have
    /// reset onboarding from the debug menu — in that case we don't want to
    /// blow away their working setup, just present the same values.
    private func loadExistingConfig() async {
        guard let cfg = try? await sidecar.getConfig() else { return }
        let existing = cfg.lmStudio.url.trimmingCharacters(in: .whitespaces)
        if !existing.isEmpty {
            url = existing
        }
        embeddingURL = cfg.lmStudio.embeddingUrl
        if !cfg.lmStudio.modelDefault.isEmpty {
            modelDefault = cfg.lmStudio.modelDefault
        }
        if !cfg.lmStudio.embeddingModel.isEmpty {
            embeddingModel = cfg.lmStudio.embeddingModel
        }
        await refreshInferenceModels()
    }

    // MARK: - Auto-detect

    /// Probes a few common loopback ports in parallel and adopts the first
    /// host that exposes `/v1/models`. On success, refreshes the model list
    /// so the picker is populated; on failure, reports it inline.
    private func autoDetect() async {
        status = .detecting
        let candidates = Self.detectionPorts.map { "http://localhost:\($0)" }
        let found: String? = await withTaskGroup(of: (String, [String]).self) { group in
            for candidate in candidates {
                group.addTask {
                    let models = await Self.fetchModelIDs(baseURL: candidate)
                    return (candidate, models)
                }
            }
            // Walk results as they arrive — the first reachable host wins,
            // and we cancel the rest to avoid lingering background calls.
            for await (host, models) in group {
                if !models.isEmpty {
                    group.cancelAll()
                    // Stash the model list so we don't re-fetch right after.
                    await MainActor.run {
                        self.inferenceModelOptions = models
                    }
                    return host
                }
            }
            return nil
        }
        if let found {
            url = found
            status = .idle
        } else {
            status = .error("No local model server reachable on ports 1234, 8080 or 11434.")
        }
    }

    private func refreshInferenceModels(force: Bool = false) async {
        let trimmed = url.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmed.isEmpty else { return }
        if !force, !inferenceModelOptions.isEmpty { return }
        status = .fetchingModels
        let models = await Self.fetchModelIDs(baseURL: trimmed, token: token)
        inferenceModelOptions = models
        if status == .fetchingModels { status = .idle }
    }

    private func refreshEmbeddingModels(force: Bool = false) async {
        let raw = embeddingURL.trimmingCharacters(in: .whitespacesAndNewlines)
        let target = raw.isEmpty
            ? url.trimmingCharacters(in: .whitespacesAndNewlines)
            : raw
        guard !target.isEmpty else { return }
        if !force, !embeddingModelOptions.isEmpty { return }
        embeddingModelOptions = await Self.fetchModelIDs(baseURL: target, token: token)
    }

    // MARK: - Test + persist

    /// Validates the chosen URL/model with a short live inference, persists
    /// the config to the sidecar, and bounces the supervisor so the new
    /// endpoint takes effect before the user reaches the chat. Failures are
    /// surfaced inline; the parent footer's "Skip for now" stays available.
    private func testAndContinue() async {
        let trimmedURL = url.trimmingCharacters(in: .whitespacesAndNewlines)
        let trimmedModel = modelDefault.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmedURL.isEmpty, !trimmedModel.isEmpty else { return }

        status = .testing
        if let testError = await Self.testInference(baseURL: trimmedURL, token: token, model: trimmedModel) {
            status = .error(testError)
            return
        }

        status = .saving
        let patch = SidecarConfigPatch(
            lmStudio: .init(
                url: trimmedURL,
                embeddingUrl: embeddingURL.trimmingCharacters(in: .whitespaces).isEmpty
                    ? nil
                    : embeddingURL.trimmingCharacters(in: .whitespaces),
                modelDefault: trimmedModel,
                embeddingModel: embeddingModel.trimmingCharacters(in: .whitespaces).isEmpty
                    ? nil
                    : embeddingModel.trimmingCharacters(in: .whitespaces)
            )
        )
        do {
            try await sidecar.patchConfig(patch)
        } catch {
            status = .error("Saved test passed, but the sidecar refused the config: \(error.localizedDescription)")
            return
        }

        if supervisor.isRunning {
            status = .restarting
            await supervisor.restart()
        }

        status = .success
        // Tiny pause so the user sees the success state register before the
        // sheet animates to the next step.
        try? await Task.sleep(nanoseconds: 350_000_000)
        onAdvance()
    }

    // MARK: - Probes

    /// Probe `/v1/models` and return the list of model IDs. Empty array is
    /// the "unreachable / not OpenAI-compat" sentinel — callers treat it as
    /// failure for auto-detect, but a free-form input still works.
    private static func fetchModelIDs(baseURL raw: String, token: String = "") async -> [String] {
        guard let base = URL(string: raw) else { return [] }
        let endpoint = base.appendingPathComponent("v1/models")
        var request = URLRequest(url: endpoint)
        request.httpMethod = "GET"
        request.timeoutInterval = 3
        request.setValue("application/json", forHTTPHeaderField: "Accept")
        let trimmedToken = token.trimmingCharacters(in: .whitespaces)
        if !trimmedToken.isEmpty {
            request.setValue("Bearer \(trimmedToken)", forHTTPHeaderField: "Authorization")
        }
        do {
            let (data, response) = try await URLSession.shared.data(for: request)
            guard let http = response as? HTTPURLResponse, (200...299).contains(http.statusCode) else {
                return []
            }
            struct ModelsResponse: Decodable {
                struct Model: Decodable { let id: String }
                let data: [Model]
            }
            return try JSONDecoder().decode(ModelsResponse.self, from: data).data.map(\.id)
        } catch {
            return []
        }
    }

    /// Issue a short `Hello` round-trip against `/v1/chat/completions`. Returns
    /// `nil` on success, a human-readable error string on failure.
    private static func testInference(baseURL raw: String, token: String, model: String) async -> String? {
        guard let base = URL(string: raw) else {
            return "Invalid URL — make sure it starts with http:// or https://"
        }
        let endpoint = base.appendingPathComponent("v1/chat/completions")
        var request = URLRequest(url: endpoint)
        request.httpMethod = "POST"
        request.timeoutInterval = 30
        request.setValue("application/json", forHTTPHeaderField: "Content-Type")
        request.setValue("application/json", forHTTPHeaderField: "Accept")
        let trimmedToken = token.trimmingCharacters(in: .whitespaces)
        if !trimmedToken.isEmpty {
            request.setValue("Bearer \(trimmedToken)", forHTTPHeaderField: "Authorization")
        }
        let payload: [String: Any] = [
            "model": model,
            "messages": [["role": "user", "content": "Hello"]],
            "max_tokens": 16,
            "stream": false
        ]
        request.httpBody = try? JSONSerialization.data(withJSONObject: payload)

        do {
            let (data, response) = try await URLSession.shared.data(for: request)
            guard let http = response as? HTTPURLResponse else {
                return "No response from the server."
            }
            if !(200...299).contains(http.statusCode) {
                let detail = String(data: data, encoding: .utf8)?
                    .prefix(180)
                    .trimmingCharacters(in: .whitespacesAndNewlines)
                return "HTTP \(http.statusCode)\(detail.map { " — \($0)" } ?? "")"
            }
            return nil
        } catch {
            return "Could not reach \(raw): \(error.localizedDescription)"
        }
    }
}
