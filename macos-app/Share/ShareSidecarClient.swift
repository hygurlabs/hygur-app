import Foundation

/// Self-contained HTTP client for the Share Extension. Mirrors the small
/// subset of `SharedSidecarClient` the extension needs — duplicated here
/// rather than imported because share extensions can't pull in the main
/// app's Swift modules without a framework target, and a framework target
/// is overkill for ~30 lines of HTTP.
///
/// Reads the sidecar URL + token from the App Group `UserDefaults` suite
/// the main app writes to at launch.
enum ShareSidecarClient {

    static let appGroup = "group.com.hygur.shared"
    static let urlKey = "hygur.shared.sidecarURL"
    static let tokenKey = "hygur.shared.sidecarToken"

    enum IngestError: Error {
        case sidecarUnreachable
        case unauthorized
        case server(status: Int, message: String?)
        case transport(Error)
        case invalidPayload

        var userFacingMessage: String {
            switch self {
            case .sidecarUnreachable:
                return "Hygur sidecar isn't reachable. Open Hygur first."
            case .unauthorized:
                return "Hygur authentication failed. Open the app to refresh the token."
            case .server(let status, let message):
                if let message, !message.isEmpty {
                    return "Sidecar error \(status): \(message)"
                }
                return "Sidecar returned status \(status)."
            case .transport(let error):
                return "Network error: \(error.localizedDescription)"
            case .invalidPayload:
                return "Couldn't read the captured content."
            }
        }
    }

    // MARK: - Public API

    static func ingestURL(_ url: URL, title: String, tags: [String]) async throws {
        let config = readConfig()
        try await assertHealthy(baseURL: config.url)
        let noteTitle: String
        if !title.trimmingCharacters(in: .whitespaces).isEmpty {
            noteTitle = String(title.prefix(120))
        } else {
            noteTitle = "Link: \(url.host ?? url.absoluteString)"
        }
        let body = "Source: \(url.absoluteString)\n"
        try await postNote(baseURL: config.url, token: config.token, title: noteTitle, content: body, tags: tags)
    }

    static func ingestText(_ text: String, tags: [String]) async throws {
        let config = readConfig()
        try await assertHealthy(baseURL: config.url)
        let firstLine = text
            .split(whereSeparator: \.isNewline)
            .first
            .map(String.init)?
            .trimmingCharacters(in: .whitespaces) ?? ""
        let title = firstLine.isEmpty ? "Captured note" : String(firstLine.prefix(80))
        try await postNote(baseURL: config.url, token: config.token, title: title, content: text, tags: tags)
    }

    static func ingestFile(at absolutePath: String, tags: [String]) async throws {
        let config = readConfig()
        try await assertHealthy(baseURL: config.url)

        let url = config.url.appendingPathComponent("knowledge/ingest")
        var request = URLRequest(url: url)
        request.httpMethod = "POST"
        request.setValue("application/json", forHTTPHeaderField: "Content-Type")
        if let token = config.token { request.setValue(token, forHTTPHeaderField: "X-Hygur-Token") }

        struct IngestBody: Encodable {
            let path: String
            let tags: [String]?
        }
        request.httpBody = try JSONEncoder().encode(
            IngestBody(path: absolutePath, tags: tags.isEmpty ? nil : tags)
        )

        try await send(request: request)
    }

    // MARK: - Internals

    private static func readConfig() -> (url: URL, token: String?) {
        let defaults = UserDefaults(suiteName: appGroup)
        let urlString = defaults?.string(forKey: urlKey) ?? "http://localhost:8420"
        let url = URL(string: urlString) ?? URL(string: "http://localhost:8420")!
        return (url, defaults?.string(forKey: tokenKey))
    }

    private static func assertHealthy(baseURL: URL, timeout: TimeInterval = 1.5) async throws {
        var request = URLRequest(url: baseURL.appendingPathComponent("health"))
        request.httpMethod = "GET"
        request.timeoutInterval = timeout

        do {
            let (_, response) = try await URLSession.shared.data(for: request)
            guard let http = response as? HTTPURLResponse, http.statusCode == 200 else {
                throw IngestError.sidecarUnreachable
            }
        } catch let error as IngestError {
            throw error
        } catch {
            throw IngestError.sidecarUnreachable
        }
    }

    private static func postNote(
        baseURL: URL,
        token: String?,
        title: String,
        content: String,
        tags: [String]
    ) async throws {
        let url = baseURL.appendingPathComponent("notes")
        var request = URLRequest(url: url)
        request.httpMethod = "POST"
        request.setValue("application/json", forHTTPHeaderField: "Content-Type")
        if let token { request.setValue(token, forHTTPHeaderField: "X-Hygur-Token") }

        struct NoteBody: Encodable {
            let title: String
            let content: String
            let tag_ids: [String]?
        }
        request.httpBody = try JSONEncoder().encode(
            NoteBody(title: title, content: content, tag_ids: tags.isEmpty ? nil : tags)
        )

        try await send(request: request)
    }

    private static func send(request: URLRequest) async throws {
        let data: Data
        let response: URLResponse
        do {
            (data, response) = try await URLSession.shared.data(for: request)
        } catch {
            throw IngestError.transport(error)
        }

        guard let http = response as? HTTPURLResponse else {
            throw IngestError.server(status: 0, message: nil)
        }

        switch http.statusCode {
        case 200..<300:
            return
        case 401, 403:
            throw IngestError.unauthorized
        default:
            throw IngestError.server(
                status: http.statusCode,
                message: String(data: data, encoding: .utf8)
            )
        }
    }
}
