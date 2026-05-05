import SwiftUI

struct ModelSelectorView: View {
    @State private var models: [ModelInfo] = []
    @State private var selectedModelID: String = AppPreferences.shared.defaultModel
    @State private var isLoading = true
    @State private var error: String?

    private let sidecarService = SidecarService.fromSettings()

    var body: some View {
        Group {
            if isLoading {
                HStack(spacing: 6) {
                    ProgressView()
                        .controlSize(.small)
                    Text("Loading models...")
                        .foregroundStyle(.secondary)
                        .font(.caption)
                }
            } else if let error = error {
                Menu {
                    Button("Retry") {
                        Task { await loadModels() }
                    }
                } label: {
                    HStack(spacing: 4) {
                        Image(systemName: "exclamationmark.triangle.fill")
                            .foregroundStyle(.orange)
                        Text("Error")
                            .foregroundStyle(.secondary)
                    }
                }
                .help(error)
            } else if models.isEmpty {
                Menu {
                    Text("No models available")
                    Divider()
                    Text("Load a model in LM Studio")
                        .foregroundStyle(.secondary)
                    Divider()
                    Button("Refresh") {
                        Task { await loadModels() }
                    }
                } label: {
                    HStack(spacing: 4) {
                        Image(systemName: "cube.transparent")
                            .foregroundStyle(.secondary)
                        Text("No Model")
                            .foregroundStyle(.secondary)
                    }
                }
                .help("No models available. Load a model in LM Studio.")
            } else {
                Picker("Model", selection: $selectedModelID) {
                    ForEach(models) { model in
                        modelLabel(for: model)
                            .tag(model.id)
                    }
                }
                .pickerStyle(.menu)
                .labelsHidden()
                .onChange(of: selectedModelID) { _, newValue in
                    AppPreferences.shared.defaultModel = newValue
                }
            }
        }
        .task {
            await loadModels()
        }
    }

    @ViewBuilder
    private func modelLabel(for model: ModelInfo) -> some View {
        if let ctx = model.ctxWindow, ctx > 0 {
            Text("\(model.name) (\(formatContextWindow(ctx)))")
        } else {
            Text(model.name)
        }
    }

    private func formatContextWindow(_ tokens: Int) -> String {
        if tokens >= 1024 {
            return "\(tokens / 1024)K"
        }
        return "\(tokens)"
    }

    private func loadModels() async {
        isLoading = true
        error = nil

        do {
            models = try await sidecarService.models()

            // Si le modele selectionne n'existe plus, ou aucun selectionne, prendre le premier
            let modelExists = models.contains { $0.id == selectedModelID }
            if (selectedModelID.isEmpty || !modelExists) && !models.isEmpty {
                selectedModelID = models[0].id
                AppPreferences.shared.defaultModel = selectedModelID
            }
        } catch {
            self.error = "Failed to load models: \(error.localizedDescription)"
        }

        isLoading = false
    }
}

// MARK: - Expanded View for Settings

struct ModelSelectorExpandedView: View {
    @State private var models: [ModelInfo] = []
    @State private var selectedModelID: String = AppPreferences.shared.defaultModel
    @State private var isLoading = true
    @State private var error: String?

    private let sidecarService = SidecarService.fromSettings()

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            HStack {
                Text("Default Model")
                    .font(.headline)
                Spacer()
                if !isLoading {
                    Button {
                        Task { await loadModels() }
                    } label: {
                        Image(systemName: "arrow.clockwise")
                    }
                    .buttonStyle(.borderless)
                    .help("Refresh models")
                }
            }

            if isLoading {
                HStack {
                    ProgressView()
                        .controlSize(.small)
                    Text("Loading models from LM Studio...")
                        .foregroundStyle(.secondary)
                }
            } else if let error = error {
                VStack(alignment: .leading, spacing: 8) {
                    HStack {
                        Image(systemName: "exclamationmark.triangle.fill")
                            .foregroundStyle(.orange)
                        Text("Connection Error")
                            .foregroundStyle(.primary)
                    }
                    Text(error)
                        .font(.caption)
                        .foregroundStyle(.secondary)
                    Button("Retry") {
                        Task { await loadModels() }
                    }
                    .buttonStyle(.bordered)
                }
            } else if models.isEmpty {
                VStack(alignment: .leading, spacing: 8) {
                    HStack {
                        Image(systemName: "cube.transparent")
                            .foregroundStyle(.secondary)
                        Text("No models available")
                            .foregroundStyle(.secondary)
                    }
                    Text("Load a model in LM Studio to get started.")
                        .font(.caption)
                        .foregroundStyle(.secondary)
                }
            } else {
                Picker("Model", selection: $selectedModelID) {
                    ForEach(models) { model in
                        HStack {
                            Text(model.name)
                            if let ctx = model.ctxWindow, ctx > 0 {
                                Spacer()
                                Text(formatContextWindow(ctx))
                                    .foregroundStyle(.secondary)
                                    .font(.caption)
                            }
                        }
                        .tag(model.id)
                    }
                }
                .pickerStyle(.menu)
                .labelsHidden()
                .onChange(of: selectedModelID) { _, newValue in
                    AppPreferences.shared.defaultModel = newValue
                }

                if let selectedModel = models.first(where: { $0.id == selectedModelID }),
                   let ctx = selectedModel.ctxWindow, ctx > 0 {
                    Text("Context window: \(formatContextWindow(ctx))")
                        .font(.caption)
                        .foregroundStyle(.secondary)
                }
            }
        }
        .task {
            await loadModels()
        }
    }

    private func formatContextWindow(_ tokens: Int) -> String {
        if tokens >= 1024 {
            return "\(tokens / 1024)K context"
        }
        return "\(tokens) tokens"
    }

    private func loadModels() async {
        isLoading = true
        error = nil

        do {
            models = try await sidecarService.models()

            let modelExists = models.contains { $0.id == selectedModelID }
            if (selectedModelID.isEmpty || !modelExists) && !models.isEmpty {
                selectedModelID = models[0].id
                AppPreferences.shared.defaultModel = selectedModelID
            }
        } catch {
            self.error = error.localizedDescription
        }

        isLoading = false
    }
}

#Preview("Toolbar Selector") {
    ModelSelectorView()
        .padding()
}

#Preview("Expanded View") {
    ModelSelectorExpandedView()
        .padding()
        .frame(width: 300)
}
