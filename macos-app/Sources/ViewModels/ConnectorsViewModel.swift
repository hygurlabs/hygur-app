import Foundation
import SwiftUI

@MainActor
@Observable
final class ConnectorsViewModel {
    var connectors: [ConnectorSummary] = []
    var selectedConnector: ConnectorDetail?
    var isLoading = false
    var error: String?

    private var currentPollingId: String? = nil
    private let healthPollInterval: Duration = .seconds(30)

    // Internal access intentional: ConnectorConfigForm needs OAuth URL fetch
    let service: SidecarService

    init(service: SidecarService = .fromSettings()) {
        self.service = service
    }

    // MARK: - List

    func load() async {
        isLoading = true
        error = nil
        defer { isLoading = false }

        do {
            connectors = try await service.listConnectors()
        } catch {
            self.error = error.localizedDescription
        }
    }

    // MARK: - Detail

    func loadDetail(id: String) async {
        do {
            selectedConnector = try await service.getConnector(id)
        } catch {
            self.error = error.localizedDescription
        }
    }

    // MARK: - Enable / Disable

    func enable(id: String) async {
        do {
            try await service.enableConnector(id)
            await load()
            await loadDetail(id: id)
        } catch {
            self.error = error.localizedDescription
        }
    }

    func disable(id: String) async {
        do {
            try await service.disableConnector(id)
            await load()
            await loadDetail(id: id)
        } catch {
            self.error = error.localizedDescription
        }
    }

    // MARK: - Sync

    func sync(id: String) async throws {
        try await service.syncConnector(id)
    }

    // MARK: - Config

    func saveConfig(id: String, config: ConnectorConfig, schema: ConnectorConfigSchema) async {
        let secretKeys = schema.groups.flatMap(\.fields)
            .filter { $0.fieldType == "secret" || $0.fieldType == "oauth" }
            .map(\.key)

        var publicSettings = config.settings
        var secrets: [String: String] = [:]
        for key in secretKeys {
            if let val = publicSettings.removeValue(forKey: key), !val.isEmpty {
                secrets[key] = val
            }
        }

        for (key, val) in secrets {
            do {
                try KeychainService.save(connectorId: id, key: key, value: val)
            } catch {
                self.error = "Failed to store secret in Keychain: \(error.localizedDescription)"
                return
            }
        }
        if !secrets.isEmpty {
            await saveCredentials(id: id, fields: secrets)
        }

        let publicConfig = ConnectorConfig(enabled: config.enabled, settings: publicSettings, schedule: config.schedule)
        do {
            try await service.configureConnector(id, config: publicConfig)
            await loadDetail(id: id)
        } catch {
            self.error = error.localizedDescription
        }
    }

    // MARK: - Bulk credential push

    /// Pushes secrets for every connector from the macOS Keychain to the sidecar.
    /// Call this once after the app launches so the sidecar can re-init connectors
    /// with credentials it does not store itself.
    func pushAllSecretsToSidecar() async {
        let summaries: [ConnectorSummary]
        if connectors.isEmpty {
            do {
                summaries = try await service.listConnectors()
            } catch {
                return
            }
        } else {
            summaries = connectors
        }

        for summary in summaries {
            do {
                let detail = try await service.getConnector(summary.info.id)
                let secrets = KeychainService.loadSecrets(connectorId: detail.info.id, schema: detail.configSchema)
                guard !secrets.isEmpty else { continue }
                try await service.saveConnectorCredentials(detail.info.id, fields: secrets)
            } catch {
                continue
            }
        }
    }

    // MARK: - Credentials

    func saveCredentials(id: String, fields: [String: String]) async {
        do {
            try await service.saveConnectorCredentials(id, fields: fields)
        } catch {
            self.error = error.localizedDescription
        }
    }

    func pushSecretsToSidecar(connectorId: String, schema: ConnectorConfigSchema) async {
        let secrets = KeychainService.loadSecrets(connectorId: connectorId, schema: schema)
        guard !secrets.isEmpty else { return }
        await saveCredentials(id: connectorId, fields: secrets)
    }

    // MARK: - Health Polling

    func startHealthPolling(id: String) async {
        guard id != currentPollingId else { return }
        currentPollingId = id
        defer {
            if currentPollingId == id { currentPollingId = nil }
        }
        while !Task.isCancelled {
            do {
                try await Task.sleep(for: healthPollInterval)
            } catch {
                // Task was cancelled; exit loop
                return
            }

            guard !Task.isCancelled else { break }

            do {
                let health = try await service.getConnectorHealth(id)
                // Update only the health of the currently displayed connector
                if var detail = selectedConnector, detail.info.id == id {
                    detail = ConnectorDetail(
                        info: detail.info,
                        capabilities: detail.capabilities,
                        configSchema: detail.configSchema,
                        config: detail.config,
                        health: health
                    )
                    selectedConnector = detail
                }
                // Also refresh the list entry's health
                if let index = connectors.firstIndex(where: { $0.id == id }) {
                    let old = connectors[index]
                    connectors[index] = ConnectorSummary(
                        info: old.info,
                        enabled: old.enabled,
                        health: health
                    )
                }
            } catch {
                // Silently ignore polling errors; they're non-critical
            }
        }
    }

    // MARK: - Multi-instance

    func createInstance(typeID: String, instanceID: String, displayName: String, settings: [String: String] = [:], schedule: String = "") async {
        do {
            try await service.createConnectorInstance(
                typeID: typeID,
                instanceID: instanceID,
                displayName: displayName,
                settings: settings,
                schedule: schedule,
                enabled: true
            )
            await load()
        } catch {
            self.error = error.localizedDescription
        }
    }

    func deleteInstance(instanceID: String) async {
        do {
            try await service.deleteConnectorInstance(instanceID)
            if selectedConnector?.info.id == instanceID {
                selectedConnector = nil
            }
            await load()
        } catch {
            self.error = error.localizedDescription
        }
    }

    // MARK: - Helpers

    func clearError() {
        error = nil
    }
}
