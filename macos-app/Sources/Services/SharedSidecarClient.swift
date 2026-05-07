import Foundation

/// Lightweight HTTP client used by the Services menu provider (and mirrored
/// in the Share Extension) to call the sidecar without dragging in the full
/// `SidecarService` actor. The full actor pulls in many dependencies, the
/// extension can't share Swift modules with the host trivially, and the
/// Services menu only needs one thing: post a captured selection to
/// `POST /notes` (and `POST /knowledge/ingest` for files).
///
/// Reads the sidecar URL + token from `SharedAppGroup`, so changes to the
/// URL in the main app's Settings are picked up automatically without any
/// extra plumbing.
enum SharedSidecarClient {
    /// What went wrong, surfaced to the user verbatim by the Services menu
    /// notification and the Share Extension confirmation panel.
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
                    return "Hygur sidecar returned \(status): \(message)"
                }
                return "Hygur sidecar returned status \(status)."
            case .transport(let underlying):
                return "Network error: \(underlying.localizedDescription)"
            case .invalidPayload:
                return "Couldn't read the captured content."
            }
        }
    }

    // MARK: - Public API

    /// Routes the captured item to the appropriate endpoint. URLs and text
    /// snippets become notes (the sidecar handles chunking + embedding for
    /// us). Image / file payloads go through the file-based ingest path.
    static func ingest(_ item: CapturedItem, tags: [String] = []) async throws {
        let config = SharedAppGroup.readSidecarConfig()

        // Verify reachability before anything else so we fail fast with a
        // clean error instead of dropping into a 60 s URLSession timeout.
        try await assertHealthy(baseURL: config.url)

        switch item {
        case .url(let url):
            let title = "Link: \(url.host ?? url.absoluteString)"
            let body = "Source: \(url.absoluteString)\n"
            try await createNote(
                baseURL: config.url,
                token: config.token,
                title: title,
                content: body,
                tagIds: tags
            )
        case .text(let text):
            let firstLine = text
                .split(whereSeparator: \.isNewline)
                .first
                .map(String.init)?
                .trimmingCharacters(in: .whitespaces) ?? "Captured note"
            let title = String(firstLine.prefix(80))
            try await createNote(
                baseURL: config.url,
                token: config.token,
                title: title.isEmpty ? "Captured note" : title,
                content: text,
                tagIds: tags
            )
        }
    }

    /// File-based ingest — used by the Share Extension when the user shares
    /// an image. Writes the file path absolute and lets the sidecar's
    /// existing image / OCR pipeline handle the rest.
    static func ingestFile(at absolutePath: String, tags: [String] = []) async throws {
        let config = SharedAppGroup.readSidecarConfig()
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
        let body = IngestBody(path: absolutePath, tags: tags.isEmpty ? nil : tags)
        request.httpBody = try JSONEncoder().encode(body)

        try await send(request: request)
    }

    // MARK: - Internals

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

    private static func createNote(
        baseURL: URL,
        token: String?,
        title: String,
        content: String,
        tagIds: [String]
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
        let body = NoteBody(
            title: title,
            content: content,
            tag_ids: tagIds.isEmpty ? nil : tagIds
        )
        request.httpBody = try JSONEncoder().encode(body)

        try await send(request: request)
    }

    /// Performs the request, mapping every non-2xx response onto the
    /// dedicated `IngestError` case. Reuses `URLSession.shared` rather than
    /// a custom session because both call sites (Services + Share) only
    /// fire one request per user action.
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
            let message = String(data: data, encoding: .utf8)
            throw IngestError.server(status: http.statusCode, message: message)
        }
    }
}
