import Foundation
import Security

// MARK: - SidecarService Actor

/// Thread-safe service for communicating with the Go sidecar via HTTP
actor SidecarService {
    private let baseURL: URL
    private let session: URLSession
    private var token: String?

    // MARK: - Initialization

    init(baseURL: URL) {
        self.baseURL = baseURL

        let configuration = URLSessionConfiguration.default
        configuration.timeoutIntervalForRequest = AppPreferences.shared.timeout
        configuration.timeoutIntervalForResource = AppPreferences.shared.timeout * 2.5
        self.session = URLSession(configuration: configuration)

        // Prefer the token file — it is written by the sidecar and is always
        // authoritative. The Keychain can hold a stale token from a previous
        // install or data-dir migration, so we only fall back to it when the
        // file does not exist yet (sidecar hasn't started for the first time).
        if let fileToken = Self.loadTokenFromFileAndCache() {
            self.token = fileToken
        } else if let keychainToken = Self.loadTokenFromKeychain(), !keychainToken.isEmpty {
            self.token = keychainToken
        }
    }

    /// Create a service instance using the current settings
    static func fromSettings() -> SidecarService {
        let url = AppPreferences.shared.sidecarURLValue ?? URL(string: "http://localhost:8420")!
        return SidecarService(baseURL: url)
    }

    // MARK: - Token Management

    /// Set the authentication token
    func setToken(_ token: String) {
        self.token = token
    }

    /// Get the current token (for testing/debugging)
    func getToken() -> String? {
        return token
    }

    /// Load token from settings and save to Keychain
    func loadAndSaveToken(from newToken: String) throws {
        self.token = newToken
        try Self.saveTokenToKeychain(newToken)
    }

    // MARK: - Keychain Operations

    private static let keychainService = "com.hygur.sidecar"
    private static let keychainAccount = "api-token"

    /// Default token file path used by the sidecar (post-migration S1 path).
    private static let tokenFilePath = "~/Library/Application Support/Hygur/token"

    /// Load token from sidecar's file and cache it in Keychain
    private static func loadTokenFromFileAndCache() -> String? {
        let expandedPath = NSString(string: tokenFilePath).expandingTildeInPath

        guard let token = try? String(contentsOfFile: expandedPath, encoding: .utf8)
            .trimmingCharacters(in: .whitespacesAndNewlines),
              !token.isEmpty else {
            return nil
        }

        // Cache in Keychain for future use
        try? saveTokenToKeychain(token)

        return token
    }

    /// Load token from macOS Keychain
    private static func loadTokenFromKeychain() -> String? {
        let query: [String: Any] = [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: keychainService,
            kSecAttrAccount as String: keychainAccount,
            kSecReturnData as String: true,
            kSecMatchLimit as String: kSecMatchLimitOne
        ]

        var result: AnyObject?
        let status = SecItemCopyMatching(query as CFDictionary, &result)

        if status == errSecSuccess, let data = result as? Data {
            return String(data: data, encoding: .utf8)
        }
        return nil
    }

    /// Save token to macOS Keychain
    static func saveTokenToKeychain(_ token: String) throws {
        guard let tokenData = token.data(using: .utf8) else {
            throw SidecarError.invalidToken
        }

        // First, try to delete any existing token
        let deleteQuery: [String: Any] = [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: keychainService,
            kSecAttrAccount as String: keychainAccount
        ]
        SecItemDelete(deleteQuery as CFDictionary)

        // Now add the new token
        let addQuery: [String: Any] = [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: keychainService,
            kSecAttrAccount as String: keychainAccount,
            kSecValueData as String: tokenData,
            kSecAttrAccessible as String: kSecAttrAccessibleWhenUnlockedThisDeviceOnly
        ]

        let status = SecItemAdd(addQuery as CFDictionary, nil)

        guard status == errSecSuccess else {
            throw SidecarError.keychainError(status: status)
        }
    }

    /// Delete token from Keychain
    static func deleteTokenFromKeychain() throws {
        let query: [String: Any] = [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: keychainService,
            kSecAttrAccount as String: keychainAccount
        ]

        let status = SecItemDelete(query as CFDictionary)

        guard status == errSecSuccess || status == errSecItemNotFound else {
            throw SidecarError.keychainError(status: status)
        }
    }

    // MARK: - Auth Header Helper

    private func addAuthHeader(_ request: inout URLRequest) {
        if let token = token {
            request.setValue(token, forHTTPHeaderField: "X-Hygur-Token")
        }
    }

    // MARK: - Health Check

    /// Check sidecar health status (public endpoint, no auth required)
    func health() async throws -> HealthResponse {
        let url = baseURL.appendingPathComponent("health")

        var request = URLRequest(url: url)
        request.httpMethod = "GET"
        request.timeoutInterval = 5

        let (data, response) = try await session.data(for: request)

        try validateResponse(response)

        return try JSONDecoder().decode(HealthResponse.self, from: data)
    }

    // MARK: - Models

    /// Fetch available models (requires auth)
    func models() async throws -> [ModelInfo] {
        let url = baseURL.appendingPathComponent("models")

        var request = URLRequest(url: url)
        request.httpMethod = "GET"
        addAuthHeader(&request)

        let (data, response) = try await session.data(for: request)

        try validateResponse(response)

        let modelsResponse = try JSONDecoder().decode(ModelsResponse.self, from: data)
        return modelsResponse.models
    }

    // MARK: - Chat Streaming

    /// Stream chat messages (requires auth, returns SSE stream)
    func streamChat(
        messages: [ChatMessage],
        model: String? = nil,
        temperature: Double? = nil,
        maxTokens: Int? = nil
    ) -> AsyncThrowingStream<String, Error> {
        AsyncThrowingStream { continuation in
            Task {
                do {
                    let url = baseURL.appendingPathComponent("chat")

                    var request = URLRequest(url: url)
                    request.httpMethod = "POST"
                    request.setValue("application/json", forHTTPHeaderField: "Content-Type")
                    request.setValue("text/event-stream", forHTTPHeaderField: "Accept")
                    await self.addAuthHeaderAsync(&request)

                    let body = ChatRequest(
                        messages: messages,
                        model: model,
                        stream: true,
                        temperature: temperature,
                        maxTokens: maxTokens,
                        recentSourceIDs: nil,
                        sessionId: nil,
                        focusScope: nil
                    )
                    request.httpBody = try JSONEncoder().encode(body)

                    let (bytes, response) = try await session.bytes(for: request)

                    // Validate HTTP response
                    if let httpResponse = response as? HTTPURLResponse {
                        guard (200...299).contains(httpResponse.statusCode) else {
                            throw SidecarError.httpError(statusCode: httpResponse.statusCode)
                        }
                    }

                    // Parse SSE stream
                    for try await line in bytes.lines {
                        // SSE format: "data: {json}"
                        if line.hasPrefix("data: ") {
                            let jsonStr = String(line.dropFirst(6))

                            // Skip empty data or heartbeat
                            if jsonStr.isEmpty || jsonStr == "[DONE]" {
                                continue
                            }

                            if let data = jsonStr.data(using: .utf8) {
                                do {
                                    let event = try JSONDecoder().decode(StreamEvent.self, from: data)
                                    if event.done {
                                        continuation.finish()
                                        return
                                    } else if let delta = event.delta {
                                        continuation.yield(delta)
                                    }
                                } catch {
                                    // Log but continue on decode errors for individual events
                                    continue
                                }
                            }
                        }
                    }

                    continuation.finish()
                } catch {
                    continuation.finish(throwing: error)
                }
            }
        }
    }

    /// Stream RAG chat messages (requires auth, returns SSE stream with RAG context)
    /// This method yields RAGContext before streaming text deltas
    func streamRAGChat(
        messages: [ChatMessage],
        model: String? = nil,
        temperature: Double? = nil,
        maxTokens: Int? = nil,
        recentSourceIDs: [String]? = nil,
        sessionId: String? = nil,
        focusScope: FocusScopePayload? = nil
    ) -> AsyncThrowingStream<ChatStreamEvent, Error> {
        AsyncThrowingStream { continuation in
            Task {
                do {
                    let url = baseURL.appendingPathComponent("chat")

                    var request = URLRequest(url: url)
                    request.httpMethod = "POST"
                    request.setValue("application/json", forHTTPHeaderField: "Content-Type")
                    request.setValue("text/event-stream", forHTTPHeaderField: "Accept")
                    await self.addAuthHeaderAsync(&request)

                    let body = ChatRequest(
                        messages: messages,
                        model: model,
                        stream: true,
                        temperature: temperature,
                        maxTokens: maxTokens,
                        recentSourceIDs: recentSourceIDs?.isEmpty == false ? recentSourceIDs : nil,
                        sessionId: sessionId,
                        focusScope: focusScope
                    )
                    request.httpBody = try JSONEncoder().encode(body)

                    let (bytes, response) = try await session.bytes(for: request)

                    // Validate HTTP response
                    if let httpResponse = response as? HTTPURLResponse {
                        guard (200...299).contains(httpResponse.statusCode) else {
                            throw SidecarError.httpError(statusCode: httpResponse.statusCode)
                        }
                    }

                    // Parse SSE stream
                    for try await line in bytes.lines {
                        // SSE format: "data: {json}"
                        if line.hasPrefix("data: ") {
                            let jsonStr = String(line.dropFirst(6))

                            // Skip empty data or heartbeat
                            if jsonStr.isEmpty || jsonStr == "[DONE]" {
                                continue
                            }

                            if let data = jsonStr.data(using: .utf8) {
                                do {
                                    let event = try JSONDecoder().decode(RAGStreamEvent.self, from: data)

                                    // Check for error event
                                    if event.type == "error", let sseError = event.error {
                                        continuation.yield(.error(sseError.message))
                                        continuation.finish()
                                        return
                                    }
                                    // Check for RAG context event
                                    else if event.type == "rag_context", let sources = event.sources {
                                        let context = RAGContext(sources: sources, intent: event.intent)
                                        continuation.yield(.ragContext(context))
                                    }
                                    // Check for tool-call event — parse extra fields directly
                                    // since arguments/result are heterogeneous JSON.
                                    else if event.type == "tool_call" {
                                        if let toolCall = decodeToolCallSSE(data) {
                                            continuation.yield(.toolCall(toolCall))
                                        }
                                    }
                                    // Check for done
                                    else if event.done == true {
                                        continuation.yield(.done(event.usage))
                                        continuation.finish()
                                        return
                                    }
                                    // Check for delta
                                    else if let delta = event.delta {
                                        continuation.yield(.delta(delta))
                                    }
                                } catch {
                                    // Try to decode as simple StreamEvent for backwards compatibility
                                    do {
                                        let simpleEvent = try JSONDecoder().decode(StreamEvent.self, from: data)
                                        if simpleEvent.done {
                                            continuation.yield(.done(nil))
                                            continuation.finish()
                                            return
                                        } else if let delta = simpleEvent.delta {
                                            continuation.yield(.delta(delta))
                                        }
                                    } catch {
                                        // Log but continue on decode errors
                                        continue
                                    }
                                }
                            }
                        }
                    }

                    continuation.finish()
                } catch {
                    continuation.finish(throwing: error)
                }
            }
        }
    }

    /// Non-streaming chat (for simple requests)
    func chat(
        messages: [ChatMessage],
        model: String? = nil,
        temperature: Double? = nil,
        maxTokens: Int? = nil
    ) async throws -> String {
        let url = baseURL.appendingPathComponent("chat")

        var request = URLRequest(url: url)
        request.httpMethod = "POST"
        request.setValue("application/json", forHTTPHeaderField: "Content-Type")
        addAuthHeader(&request)

        let body = ChatRequest(
            messages: messages,
            model: model,
            stream: false,
            temperature: temperature,
            maxTokens: maxTokens,
            recentSourceIDs: nil,
            sessionId: nil,
            focusScope: nil
        )
        request.httpBody = try JSONEncoder().encode(body)

        let (data, response) = try await session.data(for: request)

        try validateResponse(response)

        let chatResponse = try JSONDecoder().decode(ChatResponse.self, from: data)
        return chatResponse.content
    }

    // MARK: - Helper for async context

    private func addAuthHeaderAsync(_ request: inout URLRequest) async {
        if let token = token {
            request.setValue(token, forHTTPHeaderField: "X-Hygur-Token")
        }
    }

    // MARK: - Knowledge Search

    /// Search the knowledge base using semantic search (requires auth)
    func searchKnowledge(
        query: String,
        projectId: String? = nil,
        dateFrom: Date? = nil,
        dateTo: Date? = nil,
        topK: Int = 20
    ) async throws -> SearchResponse {
        let url = baseURL.appendingPathComponent("knowledge/search")

        var request = URLRequest(url: url)
        request.httpMethod = "POST"
        request.setValue("application/json", forHTTPHeaderField: "Content-Type")
        addAuthHeader(&request)

        let dateFromStr = dateFrom.map { Self.dateOnlyFormatter().string(from: $0) }
        let dateToStr = dateTo.map { Self.dateOnlyFormatter().string(from: $0) }

        let body = SearchRequest(
            query: query,
            projectId: projectId,
            dateFrom: dateFromStr,
            dateTo: dateToStr,
            topK: topK
        )
        request.httpBody = try JSONEncoder().encode(body)

        let (data, response) = try await session.data(for: request)

        try validateResponse(response)

        return try JSONDecoder().decode(SearchResponse.self, from: data)
    }

    // MARK: - Knowledge Base Management

    /// Ingest a file into the knowledge base
    func ingestFile(path: String, projectId: String? = nil, tags: [String]? = nil) async throws -> IngestResponse {
        let url = baseURL.appendingPathComponent("knowledge/ingest")

        var request = URLRequest(url: url)
        request.httpMethod = "POST"
        request.setValue("application/json", forHTTPHeaderField: "Content-Type")
        addAuthHeader(&request)

        let body = IngestRequest(path: path, projectId: projectId, tags: tags)
        request.httpBody = try JSONEncoder().encode(body)

        let (data, response) = try await session.data(for: request)
        try validateResponse(response)

        return try JSONDecoder().decode(IngestResponse.self, from: data)
    }

    /// Ingest a folder recursively into the knowledge base
    func ingestFolder(
        path: String,
        projectId: String? = nil,
        tags: [String]? = nil,
        maxDepth: Int? = nil,
        extensions: [String]? = nil,
        ignorePatterns: [String]? = nil
    ) async throws -> IngestFolderResponse {
        let url = baseURL.appendingPathComponent("knowledge/ingest-folder")

        var request = URLRequest(url: url)
        request.httpMethod = "POST"
        request.setValue("application/json", forHTTPHeaderField: "Content-Type")
        request.timeoutInterval = 300 // 5 minutes for large folder imports
        addAuthHeader(&request)

        let body = IngestFolderRequest(
            path: path,
            projectId: projectId,
            tags: tags,
            maxDepth: maxDepth,
            extensions: extensions,
            ignorePatterns: ignorePatterns
        )
        request.httpBody = try JSONEncoder().encode(body)

        let (data, response) = try await session.data(for: request)
        try validateResponse(response)

        return try JSONDecoder().decode(IngestFolderResponse.self, from: data)
    }

    /// List knowledge items with pagination
    func listKnowledgeItems(limit: Int = 50, offset: Int = 0) async throws -> KnowledgeListResponse {
        var components = URLComponents(url: baseURL.appendingPathComponent("knowledge/items"), resolvingAgainstBaseURL: false)!
        components.queryItems = [
            URLQueryItem(name: "limit", value: "\(limit)"),
            URLQueryItem(name: "offset", value: "\(offset)")
        ]

        var request = URLRequest(url: components.url!)
        request.httpMethod = "GET"
        addAuthHeader(&request)

        let (data, response) = try await session.data(for: request)
        try validateResponse(response)

        let decoder = JSONDecoder()
        decoder.dateDecodingStrategy = .iso8601
        return try decoder.decode(KnowledgeListResponse.self, from: data)
    }

    /// Get a single knowledge item by content ID
    func getKnowledgeItem(contentId: String) async throws -> KnowledgeItem? {
        let url = try Self.knowledgeItemURL(base: baseURL, contentId: contentId)

        var request = URLRequest(url: url)
        request.httpMethod = "GET"
        addAuthHeader(&request)

        let (data, response) = try await session.data(for: request)

        // Return nil for 404 (item not found)
        if let httpResponse = response as? HTTPURLResponse, httpResponse.statusCode == 404 {
            return nil
        }

        try validateResponse(response)

        let decoder = JSONDecoder()
        decoder.dateDecodingStrategy = .iso8601
        return try decoder.decode(KnowledgeItem.self, from: data)
    }

    /// Get the full knowledge item response by content ID, including the
    /// `normalized_text` field. Used by `BriefDetailView` to render the brief
    /// markdown body — the legacy `getKnowledgeItem` strips it.
    func getKnowledgeItemFull(contentId: String) async throws -> KnowledgeItemResponse? {
        let url = try Self.knowledgeItemURL(base: baseURL, contentId: contentId)

        var request = URLRequest(url: url)
        request.httpMethod = "GET"
        addAuthHeader(&request)

        let (data, response) = try await session.data(for: request)

        if let httpResponse = response as? HTTPURLResponse, httpResponse.statusCode == 404 {
            return nil
        }
        try validateResponse(response)

        return try JSONDecoder().decode(KnowledgeItemResponse.self, from: data)
    }

    /// Builds `{base}/knowledge/{content_id}` while preserving `:` in
    /// content_ids like `brief:2026-04-30`.
    ///
    /// Two Foundation gotchas this avoids:
    /// 1. `URL.appendingPathComponent` re-encodes the `%` of any already-
    ///    encoded character → `%3A` becomes `%253A`, double-broken.
    /// 2. Despite RFC 3986 listing `:` as a valid `pchar`, Foundation's
    ///    `.urlPathAllowed` set escapes it anyway. The sidecar's chi router
    ///    then reads the raw `brief%3A2026-04-30` as the path param — the
    ///    DB stores `brief:2026-04-30`, so the lookup 404s.
    ///
    /// We build the charset ourselves with `:` explicitly inserted, then
    /// concatenate the URL string so nothing further re-escapes it.
    private static let contentIdAllowedChars: CharacterSet = {
        var set = CharacterSet.urlPathAllowed
        set.insert(":")
        return set
    }()

    private static func knowledgeItemURL(base: URL, contentId: String) throws -> URL {
        let escaped = contentId.addingPercentEncoding(withAllowedCharacters: contentIdAllowedChars) ?? contentId
        let trimmedBase = base.absoluteString.hasSuffix("/")
            ? String(base.absoluteString.dropLast())
            : base.absoluteString
        guard let url = URL(string: "\(trimmedBase)/knowledge/\(escaped)") else {
            throw SidecarError.invalidResponse
        }
        return url
    }

    /// Delete a knowledge item by content ID
    func deleteKnowledgeItem(contentId: String) async throws {
        let url = baseURL.appendingPathComponent("knowledge/\(contentId)")

        var request = URLRequest(url: url)
        request.httpMethod = "DELETE"
        addAuthHeader(&request)

        let (_, response) = try await session.data(for: request)
        try validateResponse(response)
    }

    /// Add a tag to a knowledge item
    func addTagToItem(contentId: String, tagId: String) async throws -> KnowledgeItem {
        let url = baseURL.appendingPathComponent("knowledge/\(contentId)/tags")

        var request = URLRequest(url: url)
        request.httpMethod = "POST"
        request.setValue("application/json", forHTTPHeaderField: "Content-Type")
        addAuthHeader(&request)

        let body = ["tag_id": tagId]
        request.httpBody = try JSONEncoder().encode(body)

        let (data, response) = try await session.data(for: request)
        try validateResponse(response)

        let decoder = JSONDecoder()
        decoder.dateDecodingStrategy = .iso8601
        return try decoder.decode(KnowledgeItem.self, from: data)
    }

    /// Remove a tag from a knowledge item
    func removeTagFromItem(contentId: String, tagId: String) async throws -> KnowledgeItem {
        let url = baseURL.appendingPathComponent("knowledge/\(contentId)/tags/\(tagId)")

        var request = URLRequest(url: url)
        request.httpMethod = "DELETE"
        addAuthHeader(&request)

        let (data, response) = try await session.data(for: request)
        try validateResponse(response)

        let decoder = JSONDecoder()
        decoder.dateDecodingStrategy = .iso8601
        return try decoder.decode(KnowledgeItem.self, from: data)
    }

    /// Link a knowledge item to a project
    func linkItemToProject(contentId: String, projectId: String) async throws -> KnowledgeItem {
        let url = baseURL.appendingPathComponent("knowledge/\(contentId)/project")

        var request = URLRequest(url: url)
        request.httpMethod = "POST"
        request.setValue("application/json", forHTTPHeaderField: "Content-Type")
        addAuthHeader(&request)

        let body = ["project_id": projectId]
        request.httpBody = try JSONEncoder().encode(body)

        let (data, response) = try await session.data(for: request)
        try validateResponse(response)

        let decoder = JSONDecoder()
        decoder.dateDecodingStrategy = .iso8601
        return try decoder.decode(KnowledgeItem.self, from: data)
    }

    /// Unlink a knowledge item from its project
    func unlinkItemFromProject(contentId: String) async throws -> KnowledgeItem {
        let url = baseURL.appendingPathComponent("knowledge/\(contentId)/project")

        var request = URLRequest(url: url)
        request.httpMethod = "DELETE"
        addAuthHeader(&request)

        let (data, response) = try await session.data(for: request)
        try validateResponse(response)

        let decoder = JSONDecoder()
        decoder.dateDecodingStrategy = .iso8601
        return try decoder.decode(KnowledgeItem.self, from: data)
    }

    /// Reset the entire knowledge base (delete all items, chunks, vectors)
    func resetKnowledgeBase() async throws {
        let url = baseURL.appendingPathComponent("knowledge/reset")

        var request = URLRequest(url: url)
        request.httpMethod = "DELETE"
        addAuthHeader(&request)

        let (_, response) = try await session.data(for: request)
        try validateResponse(response)
    }

    // MARK: - Projects

    /// List all projects
    func listProjects() async throws -> [Project] {
        let url = baseURL.appendingPathComponent("projects")

        var request = URLRequest(url: url)
        request.httpMethod = "GET"
        addAuthHeader(&request)

        let (data, response) = try await session.data(for: request)

        try validateResponse(response)

        let decoder = JSONDecoder()
        return try decoder.decode([Project].self, from: data)
    }

    /// Create a new project
    func createProject(name: String, description: String? = nil, tags: [String]? = nil) async throws -> Project {
        let url = baseURL.appendingPathComponent("projects")

        var request = URLRequest(url: url)
        request.httpMethod = "POST"
        request.setValue("application/json", forHTTPHeaderField: "Content-Type")
        addAuthHeader(&request)

        let body = CreateProjectRequest(name: name, description: description, tags: tags)
        request.httpBody = try JSONEncoder().encode(body)

        let (data, response) = try await session.data(for: request)

        try validateResponse(response)

        let decoder = JSONDecoder()
        return try decoder.decode(Project.self, from: data)
    }

    /// Get a single project by ID
    func getProject(id: String) async throws -> Project {
        let url = baseURL.appendingPathComponent("projects").appendingPathComponent(id)

        var request = URLRequest(url: url)
        request.httpMethod = "GET"
        addAuthHeader(&request)

        let (data, response) = try await session.data(for: request)

        try validateResponse(response)

        let decoder = JSONDecoder()
        return try decoder.decode(Project.self, from: data)
    }

    /// Update an existing project
    func updateProject(
        id: String,
        name: String? = nil,
        description: String? = nil,
        tags: [String]? = nil,
        archived: Bool? = nil
    ) async throws -> Project {
        let url = baseURL.appendingPathComponent("projects").appendingPathComponent(id)

        var request = URLRequest(url: url)
        request.httpMethod = "PUT"
        request.setValue("application/json", forHTTPHeaderField: "Content-Type")
        addAuthHeader(&request)

        let body = UpdateProjectRequest(name: name, description: description, tags: tags, archived: archived)
        request.httpBody = try JSONEncoder().encode(body)

        let (data, response) = try await session.data(for: request)

        try validateResponse(response)

        let decoder = JSONDecoder()
        return try decoder.decode(Project.self, from: data)
    }

    /// Delete a project
    func deleteProject(id: String) async throws {
        let url = baseURL.appendingPathComponent("projects").appendingPathComponent(id)

        var request = URLRequest(url: url)
        request.httpMethod = "DELETE"
        addAuthHeader(&request)

        let (_, response) = try await session.data(for: request)

        try validateResponse(response)
    }

    /// Archive or unarchive a project
    func toggleProjectArchive(id: String, archived: Bool) async throws -> Project {
        return try await updateProject(id: id, archived: archived)
    }

    /// List all items in a project
    func listProjectItems(projectId: String) async throws -> [ProjectItem] {
        let url = baseURL.appendingPathComponent("projects").appendingPathComponent(projectId).appendingPathComponent("items")

        var request = URLRequest(url: url)
        request.httpMethod = "GET"
        addAuthHeader(&request)

        let (data, response) = try await session.data(for: request)

        try validateResponse(response)

        let decoder = JSONDecoder()
        let itemsResponse = try decoder.decode(ProjectItemsResponse.self, from: data)
        return itemsResponse.items
    }

    // MARK: - Graph

    /// Get the knowledge graph data (nodes and edges)
    func getGraph() async throws -> GraphResponse {
        let url = baseURL.appendingPathComponent("graph")

        var request = URLRequest(url: url)
        request.httpMethod = "GET"
        addAuthHeader(&request)

        let (data, response) = try await session.data(for: request)

        try validateResponse(response)

        return try JSONDecoder().decode(GraphResponse.self, from: data)
    }

    // MARK: - Tags

    /// List all tags
    func listTags() async throws -> [Tag] {
        let url = baseURL.appendingPathComponent("tags")

        var request = URLRequest(url: url)
        request.httpMethod = "GET"
        addAuthHeader(&request)

        let (data, response) = try await session.data(for: request)

        try validateResponse(response)

        let decoder = JSONDecoder()
        let tagResponse = try decoder.decode(TagListResponse.self, from: data)
        return tagResponse.tags
    }

    /// Create a new tag
    func createTag(name: String, color: String) async throws -> Tag {
        let url = baseURL.appendingPathComponent("tags")

        var request = URLRequest(url: url)
        request.httpMethod = "POST"
        request.setValue("application/json", forHTTPHeaderField: "Content-Type")
        addAuthHeader(&request)

        let body = CreateTagRequest(name: name, color: color)
        request.httpBody = try JSONEncoder().encode(body)

        let (data, response) = try await session.data(for: request)

        try validateResponse(response)

        let decoder = JSONDecoder()
        return try decoder.decode(Tag.self, from: data)
    }

    /// Update an existing tag
    func updateTag(id: String, name: String? = nil, color: String? = nil) async throws -> Tag {
        let url = baseURL.appendingPathComponent("tags").appendingPathComponent(id)

        var request = URLRequest(url: url)
        request.httpMethod = "PUT"
        request.setValue("application/json", forHTTPHeaderField: "Content-Type")
        addAuthHeader(&request)

        let body = UpdateTagRequest(name: name, color: color)
        request.httpBody = try JSONEncoder().encode(body)

        let (data, response) = try await session.data(for: request)

        try validateResponse(response)

        let decoder = JSONDecoder()
        return try decoder.decode(Tag.self, from: data)
    }

    /// Delete a tag
    func deleteTag(id: String) async throws {
        let url = baseURL.appendingPathComponent("tags").appendingPathComponent(id)

        var request = URLRequest(url: url)
        request.httpMethod = "DELETE"
        addAuthHeader(&request)

        let (_, response) = try await session.data(for: request)

        try validateResponse(response)
    }

    /// List all items with a specific tag
    func listTagItems(tagId: String) async throws -> [TagItem] {
        let url = baseURL.appendingPathComponent("tags").appendingPathComponent(tagId).appendingPathComponent("items")

        var request = URLRequest(url: url)
        request.httpMethod = "GET"
        addAuthHeader(&request)

        let (data, response) = try await session.data(for: request)

        try validateResponse(response)

        let decoder = JSONDecoder()
        let itemsResponse = try decoder.decode(TagItemsResponse.self, from: data)
        return itemsResponse.items
    }

    // MARK: - Notes

    /// List all notes
    func listNotes() async throws -> [Note] {
        let url = baseURL.appendingPathComponent("notes")

        var request = URLRequest(url: url)
        request.httpMethod = "GET"
        addAuthHeader(&request)

        let (data, response) = try await session.data(for: request)

        try validateResponse(response)

        let decoder = JSONDecoder()
        let noteResponse = try decoder.decode(NoteListResponse.self, from: data)
        return noteResponse.notes
    }

    /// Create a new note
    func createNote(title: String, content: String, projectId: String? = nil, tagIds: [String]? = nil) async throws -> Note {
        let url = baseURL.appendingPathComponent("notes")

        var request = URLRequest(url: url)
        request.httpMethod = "POST"
        request.setValue("application/json", forHTTPHeaderField: "Content-Type")
        addAuthHeader(&request)

        let body = CreateNoteRequest(title: title, content: content, projectId: projectId, tagIds: tagIds)
        request.httpBody = try JSONEncoder().encode(body)

        let (data, response) = try await session.data(for: request)

        try validateResponse(response)

        let decoder = JSONDecoder()
        return try decoder.decode(Note.self, from: data)
    }

    /// Get a single note by ID
    func getNote(id: String) async throws -> Note {
        let url = baseURL.appendingPathComponent("notes").appendingPathComponent(id)

        var request = URLRequest(url: url)
        request.httpMethod = "GET"
        addAuthHeader(&request)

        let (data, response) = try await session.data(for: request)

        try validateResponse(response)

        let decoder = JSONDecoder()
        return try decoder.decode(Note.self, from: data)
    }

    /// Update an existing note
    func updateNote(id: String, title: String? = nil, content: String? = nil, projectId: String? = nil, tagIds: [String]? = nil) async throws -> Note {
        let url = baseURL.appendingPathComponent("notes").appendingPathComponent(id)

        var request = URLRequest(url: url)
        request.httpMethod = "PUT"
        request.setValue("application/json", forHTTPHeaderField: "Content-Type")
        addAuthHeader(&request)

        let body = UpdateNoteRequest(title: title, content: content, projectId: projectId, tagIds: tagIds)
        request.httpBody = try JSONEncoder().encode(body)

        let (data, response) = try await session.data(for: request)

        try validateResponse(response)

        let decoder = JSONDecoder()
        return try decoder.decode(Note.self, from: data)
    }

    /// Delete a note
    func deleteNote(id: String) async throws {
        let url = baseURL.appendingPathComponent("notes").appendingPathComponent(id)

        var request = URLRequest(url: url)
        request.httpMethod = "DELETE"
        addAuthHeader(&request)

        let (_, response) = try await session.data(for: request)

        try validateResponse(response)
    }

    // MARK: - Mail

    /// List all mail sources
    func listMailSources() async throws -> [EmailSource] {
        let url = baseURL.appendingPathComponent("mail/sources")

        var request = URLRequest(url: url)
        request.httpMethod = "GET"
        addAuthHeader(&request)

        let (data, response) = try await session.data(for: request)
        try validateResponse(response)

        let sourcesResponse = try JSONDecoder().decode(MailSourcesResponse.self, from: data)
        return sourcesResponse.sources
    }


    /// List mail threads from a source (legacy: provider name).
    func listMailThreads(source: String, limit: Int, offset: Int) async throws -> [EmailThread] {
        var components = URLComponents(url: baseURL.appendingPathComponent("mail/threads"), resolvingAgainstBaseURL: false)!
        components.queryItems = [
            URLQueryItem(name: "source", value: source),
            URLQueryItem(name: "limit", value: String(limit)),
            URLQueryItem(name: "offset", value: String(offset))
        ]

        var request = URLRequest(url: components.url!)
        request.httpMethod = "GET"
        addAuthHeader(&request)

        let (data, response) = try await session.data(for: request)
        try validateResponse(response)

        let threadsResponse = try JSONDecoder().decode(MailThreadsResponse.self, from: data)
        return threadsResponse.threads
    }

    /// List mail threads scoped to a specific MailAccount, optionally filtered by label IDs.
    func listMailThreads(accountId: String, labelIDs: [String] = [], limit: Int, offset: Int) async throws -> [EmailThread] {
        var components = URLComponents(url: baseURL.appendingPathComponent("mail/threads"), resolvingAgainstBaseURL: false)!
        var queryItems = [
            URLQueryItem(name: "account_id", value: accountId),
            URLQueryItem(name: "limit", value: String(limit)),
            URLQueryItem(name: "offset", value: String(offset))
        ]
        for labelID in labelIDs {
            queryItems.append(URLQueryItem(name: "label_ids", value: labelID))
        }
        components.queryItems = queryItems

        var request = URLRequest(url: components.url!)
        request.httpMethod = "GET"
        addAuthHeader(&request)

        let (data, response) = try await session.data(for: request)
        try validateResponse(response)

        let threadsResponse = try JSONDecoder().decode(MailThreadsResponse.self, from: data)
        return threadsResponse.threads.sorted { $0.dateEnd > $1.dateEnd }
    }

    // MARK: - Multi-account mail endpoints

    /// List configured mail accounts (multi-account schema).
    func listMailAccounts() async throws -> [MailAccount] {
        let url = baseURL.appendingPathComponent("mail/accounts")
        var request = URLRequest(url: url)
        request.httpMethod = "GET"
        addAuthHeader(&request)
        let (data, response) = try await session.data(for: request)
        try validateResponse(response)
        let resp = try JSONDecoder().decode(MailAccountsResponse.self, from: data)
        return resp.accounts
    }

    /// Trigger an active connectivity check on a mail account. Server caches
    /// the result for 30 seconds.
    @discardableResult
    func verifyMailAccount(accountId: String) async throws -> MailAccountVerifyResponse {
        let path = "mail/accounts/\(accountId.urlPathEscaped)/verify"
        var request = URLRequest(url: baseURL.appendingPathComponent(path))
        request.httpMethod = "POST"
        addAuthHeader(&request)
        let (data, response) = try await session.data(for: request)
        try validateResponse(response)
        return try JSONDecoder().decode(MailAccountVerifyResponse.self, from: data)
    }

    /// Per-account stats: indexed thread count + last indexed timestamp.
    func mailAccountStats(accountId: String) async throws -> MailAccountStatsResponse {
        let path = "mail/accounts/\(accountId.urlPathEscaped)/stats"
        var request = URLRequest(url: baseURL.appendingPathComponent(path))
        request.httpMethod = "GET"
        addAuthHeader(&request)
        let (data, response) = try await session.data(for: request)
        try validateResponse(response)
        return try JSONDecoder().decode(MailAccountStatsResponse.self, from: data)
    }

    /// Trigger a full sync for a specific account, in async mode (returns
    /// 202 Accepted immediately). Subscribe to the events stream to observe
    /// completion.
    @discardableResult
    func triggerMailSync(accountId: String, full: Bool, async: Bool = true) async throws -> MailSyncAck {
        var components = URLComponents(url: baseURL.appendingPathComponent("connectors/mail/sync"), resolvingAgainstBaseURL: false)!
        if async { components.queryItems = [URLQueryItem(name: "async", value: "true")] }

        var request = URLRequest(url: components.url!)
        request.httpMethod = "POST"
        request.setValue("application/json", forHTTPHeaderField: "Content-Type")
        addAuthHeader(&request)

        var body: [String: Any] = ["account_id": accountId]
        if full { body["full"] = true }
        request.httpBody = try JSONSerialization.data(withJSONObject: body)

        let (data, response) = try await session.data(for: request)
        try validateResponse(response)

        // Async mode returns MailSyncAck; sync mode returns a SyncResult shape.
        // Use `try` so a decode failure surfaces as a real error rather than
        // silently returning a fake "completed" status.
        return try JSONDecoder().decode(MailSyncAck.self, from: data)
    }

    /// Index a mail thread into the knowledge base
    func indexMailThread(source: String, threadId: String) async throws {
        let url = baseURL.appendingPathComponent("mail/threads/\(threadId)/index")

        var request = URLRequest(url: url)
        request.httpMethod = "POST"
        request.setValue("application/json", forHTTPHeaderField: "Content-Type")
        addAuthHeader(&request)

        let body: [String: String] = ["source": source]
        request.httpBody = try JSONEncoder().encode(body)

        let (_, response) = try await session.data(for: request)
        try validateResponse(response)
    }


    /// List mail labels/folders (legacy — uses provider name, kept for ConnectorConfigForm).
    func listMailLabels(source: String) async throws -> [MailLabel] {
        var components = URLComponents(url: baseURL.appendingPathComponent("mail/labels"), resolvingAgainstBaseURL: false)!
        components.queryItems = [URLQueryItem(name: "source", value: source)]

        var request = URLRequest(url: components.url!)
        request.httpMethod = "GET"
        addAuthHeader(&request)

        let (data, response) = try await session.data(for: request)
        try validateResponse(response)

        let labelsResponse = try JSONDecoder().decode(MailLabelsResponse.self, from: data)
        return labelsResponse.labels
    }

    /// List mail labels for a specific account (account-scoped, preferred).
    func listMailLabels(accountId: String) async throws -> [MailLabel] {
        let encoded = accountId.addingPercentEncoding(withAllowedCharacters: .urlPathAllowed) ?? accountId
        let url = baseURL.appendingPathComponent("mail/accounts/\(encoded)/labels")

        var request = URLRequest(url: url)
        request.httpMethod = "GET"
        addAuthHeader(&request)

        let (data, response) = try await session.data(for: request)
        try validateResponse(response)

        let labelsResponse = try JSONDecoder().decode(MailLabelsResponse.self, from: data)
        return labelsResponse.labels
    }

    /// List mailboxes for a source
    func listMailboxes(source: String) async throws -> [String] {
        var components = URLComponents(url: baseURL.appendingPathComponent("mail/mailboxes"), resolvingAgainstBaseURL: false)!
        components.queryItems = [URLQueryItem(name: "source", value: source)]

        var request = URLRequest(url: components.url!)
        request.httpMethod = "GET"
        addAuthHeader(&request)

        let (data, response) = try await session.data(for: request)
        try validateResponse(response)

        struct MailboxesResponse: Codable {
            let mailboxes: [String]
        }

        let mailboxesResponse = try JSONDecoder().decode(MailboxesResponse.self, from: data)
        return mailboxesResponse.mailboxes
    }

    /// Summarize a mail thread using AI
    func summarizeMailThread(source: String, threadId: String, model: String) async throws -> EmailSummary {
        let encodedThreadId = threadId.addingPercentEncoding(withAllowedCharacters: .urlPathAllowed) ?? threadId
        let url = baseURL.appendingPathComponent("mail/threads/\(encodedThreadId)/summarize")

        var request = URLRequest(url: url)
        request.httpMethod = "POST"
        request.setValue("application/json", forHTTPHeaderField: "Content-Type")
        addAuthHeader(&request)

        let body = SummarizeMailThreadRequest(source: source, model: model)
        request.httpBody = try JSONEncoder().encode(body)

        let (data, response) = try await session.data(for: request)
        try validateResponse(response)

        return try JSONDecoder().decode(EmailSummary.self, from: data)
    }

    // MARK: - Connectors

    func listConnectors() async throws -> [ConnectorSummary] {
        var request = URLRequest(url: baseURL.appendingPathComponent("connectors"))
        request.httpMethod = "GET"
        addAuthHeader(&request)
        let (data, response) = try await session.data(for: request)
        try validateResponse(response)
        let decoder = JSONDecoder()
        return try decoder.decode([ConnectorSummary].self, from: data)
    }

    func getConnector(_ id: String) async throws -> ConnectorDetail {
        var request = URLRequest(url: baseURL.appendingPathComponent("connectors/\(id)"))
        request.httpMethod = "GET"
        addAuthHeader(&request)
        let (data, response) = try await session.data(for: request)
        try validateResponse(response)
        let decoder = JSONDecoder()
        return try decoder.decode(ConnectorDetail.self, from: data)
    }

    func getConnectorHealth(_ id: String) async throws -> ConnectorHealth {
        var request = URLRequest(url: baseURL.appendingPathComponent("connectors/\(id)/health"))
        request.httpMethod = "GET"
        addAuthHeader(&request)
        let (data, response) = try await session.data(for: request)
        try validateResponse(response)
        let decoder = JSONDecoder()
        return try decoder.decode(ConnectorHealth.self, from: data)
    }

    func enableConnector(_ id: String) async throws {
        var request = URLRequest(url: baseURL.appendingPathComponent("connectors/\(id)/enable"))
        request.httpMethod = "POST"
        addAuthHeader(&request)
        let (_, response) = try await session.data(for: request)
        try validateResponse(response)
    }

    func disableConnector(_ id: String) async throws {
        var request = URLRequest(url: baseURL.appendingPathComponent("connectors/\(id)/disable"))
        request.httpMethod = "POST"
        addAuthHeader(&request)
        let (_, response) = try await session.data(for: request)
        try validateResponse(response)
    }

    func fetchMailboxes(connectorId: String) async throws -> [String] {
        var request = URLRequest(url: baseURL.appendingPathComponent("connectors/\(connectorId)/mailboxes"))
        request.httpMethod = "GET"
        addAuthHeader(&request)
        let (data, response) = try await session.data(for: request)
        try validateResponse(response)
        return try JSONDecoder().decode([String].self, from: data)
    }

    func fetchLabels(connectorId: String) async throws -> [MailboxOption] {
        var request = URLRequest(url: baseURL.appendingPathComponent("connectors/\(connectorId)/labels"))
        request.httpMethod = "GET"
        addAuthHeader(&request)
        let (data, response) = try await session.data(for: request)
        try validateResponse(response)
        return try JSONDecoder().decode([MailboxOption].self, from: data)
    }

    /// Triggers a connector sync in async mode (returns 202 immediately).
    /// The sync runs in a background goroutine on the sidecar with a 10-minute
    /// budget — no URLSession timeout can interrupt it mid-embedding.
    func syncConnector(_ id: String, mailbox: String = "", limit: Int = 0) async throws {
        var components = URLComponents(url: baseURL.appendingPathComponent("connectors/\(id)/sync"), resolvingAgainstBaseURL: false)!
        components.queryItems = [URLQueryItem(name: "async", value: "true")]
        var request = URLRequest(url: components.url!)
        request.httpMethod = "POST"
        request.setValue("application/json", forHTTPHeaderField: "Content-Type")
        var opts: [String: Any] = [:]
        if !mailbox.isEmpty { opts["mailbox"] = mailbox }
        if limit > 0 { opts["limit"] = limit }
        request.httpBody = try JSONSerialization.data(withJSONObject: opts)
        addAuthHeader(&request)
        let (_, response) = try await session.data(for: request)
        try validateResponse(response)
    }

    func authConnectorCallback(_ id: String, code: String) async throws {
        var request = URLRequest(url: baseURL.appendingPathComponent("connectors/\(id)/auth/callback"))
        request.httpMethod = "POST"
        request.setValue("application/json", forHTTPHeaderField: "Content-Type")
        request.httpBody = try JSONEncoder().encode(["code": code])
        addAuthHeader(&request)
        let (_, response) = try await session.data(for: request)
        try validateResponse(response)
    }

    func configureConnector(_ id: String, config: ConnectorConfig) async throws {
        var request = URLRequest(url: baseURL.appendingPathComponent("connectors/\(id)/config"))
        request.httpMethod = "PUT"
        request.setValue("application/json", forHTTPHeaderField: "Content-Type")
        request.httpBody = try JSONEncoder().encode(config)
        addAuthHeader(&request)
        let (_, response) = try await session.data(for: request)
        try validateResponse(response)
    }

    func saveConnectorCredentials(_ id: String, fields: [String: String]) async throws {
        var request = URLRequest(url: baseURL.appendingPathComponent("connectors/\(id)/credentials"))
        request.httpMethod = "PUT"
        request.setValue("application/json", forHTTPHeaderField: "Content-Type")
        request.httpBody = try JSONEncoder().encode(fields)
        addAuthHeader(&request)
        let (_, response) = try await session.data(for: request)
        // 204 is success
        guard let httpResponse = response as? HTTPURLResponse,
              (200...299).contains(httpResponse.statusCode) else {
            throw SidecarError.httpError(statusCode: (response as? HTTPURLResponse)?.statusCode ?? 0)
        }
    }

    func getConnectorAuthURL(_ id: String) async throws -> String {
        var request = URLRequest(url: baseURL.appendingPathComponent("connectors/\(id)/auth/url"))
        request.httpMethod = "GET"
        addAuthHeader(&request)
        let (data, response) = try await session.data(for: request)
        try validateResponse(response)
        struct AuthURLResponse: Codable { let url: String }
        let decoded = try JSONDecoder().decode(AuthURLResponse.self, from: data)
        return decoded.url
    }

    // MARK: - Marketplace

    func listMarketplaceConnectors() async throws -> [MarketplaceListing] {
        var request = URLRequest(url: baseURL.appendingPathComponent("marketplace/connectors"))
        request.httpMethod = "GET"
        addAuthHeader(&request)
        let (data, response) = try await session.data(for: request)
        try validateResponse(response)
        return try JSONDecoder().decode([MarketplaceListing].self, from: data)
    }

    func installMarketplaceConnector(typeID: String) async throws {
        var request = URLRequest(url: baseURL.appendingPathComponent("marketplace/install/\(typeID)"))
        request.httpMethod = "POST"
        addAuthHeader(&request)
        let (_, response) = try await session.data(for: request)
        try validateResponse(response)
    }

    // MARK: - Connector Instances (multi-compte)

    func listConnectorInstances() async throws -> [ConnectorInstanceInfo] {
        var request = URLRequest(url: baseURL.appendingPathComponent("connectors/instances"))
        request.httpMethod = "GET"
        addAuthHeader(&request)
        let (data, response) = try await session.data(for: request)
        try validateResponse(response)
        return try JSONDecoder().decode([ConnectorInstanceInfo].self, from: data)
    }

    func createConnectorInstance(typeID: String, instanceID: String, displayName: String, settings: [String: String] = [:], schedule: String = "", enabled: Bool = false) async throws {
        var request = URLRequest(url: baseURL.appendingPathComponent("connectors/\(typeID)/instances"))
        request.httpMethod = "POST"
        request.setValue("application/json", forHTTPHeaderField: "Content-Type")
        addAuthHeader(&request)
        let body: [String: Any] = [
            "id": instanceID,
            "display_name": displayName,
            "settings": settings,
            "schedule": schedule,
            "enabled": enabled
        ]
        request.httpBody = try JSONSerialization.data(withJSONObject: body)
        let (_, response) = try await session.data(for: request)
        try validateResponse(response)
    }

    func deleteConnectorInstance(_ instanceID: String) async throws {
        var request = URLRequest(url: baseURL.appendingPathComponent("connectors/instances/\(instanceID)"))
        request.httpMethod = "DELETE"
        addAuthHeader(&request)
        let (_, response) = try await session.data(for: request)
        try validateResponse(response)
    }

    // MARK: - Event Stream

    /// Subscribe to the sidecar SSE event stream (GET /events).
    /// Yields decoded SidecarEvent values until the connection drops or is cancelled.
    func streamEvents() -> AsyncThrowingStream<SidecarEvent, Error> {
        AsyncThrowingStream { continuation in
            Task {
                do {
                    let url = baseURL.appendingPathComponent("events")
                    var request = URLRequest(url: url)
                    request.httpMethod = "GET"
                    request.setValue("text/event-stream", forHTTPHeaderField: "Accept")
                    // Per-byte read timeout. The sidecar emits a `: keepalive`
                    // comment every 15 s, so 45 s of silence implies the
                    // connection has actually died — fail fast and let the
                    // EventStreamService reconnect-with-backoff loop take over.
                    // Without this, URLSession.bytes can hang indefinitely
                    // after a sidecar restart even though no events flow.
                    request.timeoutInterval = 45
                    await self.addAuthHeaderAsync(&request)

                    let (bytes, response) = try await session.bytes(for: request)

                    if let http = response as? HTTPURLResponse,
                       !(200...299).contains(http.statusCode) {
                        continuation.finish(throwing: SidecarError.httpError(statusCode: http.statusCode))
                        return
                    }

                    for try await line in bytes.lines {
                        if line.hasPrefix("data: "),
                           let data = String(line.dropFirst(6)).data(using: .utf8),
                           let event = try? JSONDecoder().decode(SidecarEvent.self, from: data) {
                            continuation.yield(event)
                        }
                    }
                    continuation.finish()
                } catch {
                    continuation.finish(throwing: error)
                }
            }
        }
    }

    // MARK: - Helper

    // ISO8601DateFormatter is not Sendable, so we create it per-call.
    private static func iso8601DateFormatter() -> ISO8601DateFormatter {
        let formatter = ISO8601DateFormatter()
        formatter.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        return formatter
    }

    private static func dateOnlyFormatter() -> DateFormatter {
        let f = DateFormatter()
        f.dateFormat = "yyyy-MM-dd"
        f.locale = Locale(identifier: "en_US_POSIX")
        f.timeZone = TimeZone(identifier: "UTC")
        return f
    }

    // MARK: - Response Validation

    private func validateResponse(_ response: URLResponse) throws {
        guard let httpResponse = response as? HTTPURLResponse else {
            throw SidecarError.invalidResponse
        }

        switch httpResponse.statusCode {
        case 200...299:
            return
        case 401:
            throw SidecarError.unauthorized
        case 503:
            throw SidecarError.serviceUnavailable
        default:
            throw SidecarError.httpError(statusCode: httpResponse.statusCode)
        }
    }

    // MARK: - Brief

    /// Triggers an on-demand brief on the sidecar. Returns immediately —
    /// the brief itself runs asynchronously and lands in `/events` as a
    /// `brief` event 10–30 s later (LLM dependent).
    ///
    /// - Parameters:
    ///   - projectId: When non-nil, scopes the brief to items linked to
    ///     that project — typically used as meeting prep.
    ///   - lookbackHours: Override the default 24 h activity window. Ignored
    ///     when `projectId` is set.
    func runBrief(projectId: String? = nil, lookbackHours: Int? = nil) async throws -> BriefRunResponse {
        let url = baseURL.appendingPathComponent("brief/run")
        var request = URLRequest(url: url)
        request.httpMethod = "POST"
        request.setValue("application/json", forHTTPHeaderField: "Content-Type")
        addAuthHeader(&request)

        let body = BriefRunRequest(projectId: projectId, lookbackHours: lookbackHours)
        request.httpBody = try JSONEncoder().encode(body)

        let (data, response) = try await session.data(for: request)
        try validateResponse(response)
        return try JSONDecoder().decode(BriefRunResponse.self, from: data)
    }

    // MARK: - Sidecar Config

    /// Fetch the current tunable sidecar configuration (GET /config).
    func getConfig() async throws -> SidecarConfig {
        var request = URLRequest(url: baseURL.appendingPathComponent("config"))
        request.httpMethod = "GET"
        addAuthHeader(&request)
        let (data, response) = try await session.data(for: request)
        try validateResponse(response)
        return try JSONDecoder().decode(SidecarConfig.self, from: data)
    }

    /// Persist a partial config update (PATCH /config).
    /// Only the fields explicitly set in `patch` are written to config.yaml.
    func patchConfig(_ patch: SidecarConfigPatch) async throws {
        var request = URLRequest(url: baseURL.appendingPathComponent("config"))
        request.httpMethod = "PATCH"
        request.setValue("application/json", forHTTPHeaderField: "Content-Type")
        addAuthHeader(&request)
        request.httpBody = try JSONEncoder().encode(patch)
        let (_, response) = try await session.data(for: request)
        guard let http = response as? HTTPURLResponse, http.statusCode == 204 else {
            try validateResponse(response)
            return
        }
    }

    // MARK: - Memories

    /// Fetch every persistent memory the sidecar knows about. Sorted in the
    /// order returned by the backend (created_at ascending — the view layer
    /// reverses for newest-first display).
    func listMemories() async throws -> [MemoryItem] {
        let url = baseURL.appendingPathComponent("memory/list")
        var request = URLRequest(url: url)
        request.httpMethod = "GET"
        addAuthHeader(&request)
        let (data, response) = try await session.data(for: request)
        try validateResponse(response)
        return try JSONDecoder().decode(MemoryListResponse.self, from: data).memories
    }

    /// Delete a single memory by id. The endpoint returns 204 on success.
    func deleteMemory(id: String) async throws {
        let url = baseURL.appendingPathComponent("memory").appendingPathComponent(id)
        var request = URLRequest(url: url)
        request.httpMethod = "DELETE"
        addAuthHeader(&request)
        let (_, response) = try await session.data(for: request)
        try validateResponse(response)
    }

    // MARK: - Memories (Phase 3.3 long-term)

    /// List memories whose source is `extracted` and whose `accepted_at` is
    /// NULL — the queue the user reviews in the "Pending review" section of
    /// `MemoriesView`. Until accepted, these candidates are NEVER injected
    /// into chat (`SearchAccepted` filters them out).
    func listPendingMemories() async throws -> [MemoryItem] {
        let url = baseURL.appendingPathComponent("memory/pending")
        var request = URLRequest(url: url)
        request.httpMethod = "GET"
        addAuthHeader(&request)
        let (data, response) = try await session.data(for: request)
        try validateResponse(response)
        return try JSONDecoder().decode(MemoryListResponse.self, from: data).memories
    }

    /// Mark a pending memory as accepted. After this call the memory is
    /// eligible for cosine ranking and chat injection. Returns 204.
    func acceptMemory(id: String) async throws {
        let url = baseURL.appendingPathComponent("memory")
            .appendingPathComponent(id)
            .appendingPathComponent("accept")
        var request = URLRequest(url: url)
        request.httpMethod = "POST"
        addAuthHeader(&request)
        let (_, response) = try await session.data(for: request)
        try validateResponse(response)
    }

    /// Discard a pending memory. The sidecar deletes it outright (the user
    /// has rejected it). Returns 204. Manual memories should use
    /// `deleteMemory` instead — discard is for the pending review flow.
    func discardMemory(id: String) async throws {
        let url = baseURL.appendingPathComponent("memory")
            .appendingPathComponent(id)
            .appendingPathComponent("discard")
        var request = URLRequest(url: url)
        request.httpMethod = "POST"
        addAuthHeader(&request)
        let (_, response) = try await session.data(for: request)
        try validateResponse(response)
    }

    /// Trigger session-level memory extraction. The macOS app sends the
    /// transcript because the sidecar's session store is in-memory only and
    /// may not hold the full chat history at call time. Newly extracted
    /// candidates land as `source=extracted, accepted_at=NULL`.
    @discardableResult
    func extractMemories(sessionId: String, messages: [MemoryExtractMessage]) async throws -> MemoryExtractResponse {
        let url = baseURL.appendingPathComponent("memory/extract")
        var request = URLRequest(url: url)
        request.httpMethod = "POST"
        request.setValue("application/json", forHTTPHeaderField: "Content-Type")
        addAuthHeader(&request)
        request.httpBody = try JSONEncoder().encode(MemoryExtractRequest(sessionId: sessionId, messages: messages))
        let (data, response) = try await session.data(for: request)
        try validateResponse(response)
        return try JSONDecoder().decode(MemoryExtractResponse.self, from: data)
    }

    /// Counts the sidecar reports for the Settings UI: manual / extracted /
    /// pending. Cheap — single COUNT-by-source SQL query under the hood.
    func memoryStats() async throws -> MemoryStatsResponse {
        let url = baseURL.appendingPathComponent("memory/stats")
        var request = URLRequest(url: url)
        request.httpMethod = "GET"
        addAuthHeader(&request)
        let (data, response) = try await session.data(for: request)
        try validateResponse(response)
        return try JSONDecoder().decode(MemoryStatsResponse.self, from: data)
    }

    /// Wipe every `source=extracted` memory, including those already
    /// accepted. Manual memories are preserved. Used by the Settings panic
    /// switch. Returns the count actually deleted.
    @discardableResult
    func clearExtractedMemories() async throws -> Int {
        let url = baseURL.appendingPathComponent("memory/extracted")
        var request = URLRequest(url: url)
        request.httpMethod = "DELETE"
        addAuthHeader(&request)
        let (data, response) = try await session.data(for: request)
        try validateResponse(response)
        return try JSONDecoder().decode(MemoryClearExtractedResponse.self, from: data).deleted
    }

    // MARK: - Interactions (Phase 1 pair mode)

    /// Append an interaction event to the sidecar's append-only log. Errors
    /// are propagated so the caller can decide whether to surface them — the
    /// `InteractionLogger` service swallows them by design (fire-and-forget).
    func logInteraction(
        kind: String,
        refKind: String? = nil,
        refId: String? = nil,
        payload: [String: String]? = nil,
        sessionId: String? = nil
    ) async throws {
        let url = baseURL.appendingPathComponent("interactions")
        var request = URLRequest(url: url)
        request.httpMethod = "POST"
        request.setValue("application/json", forHTTPHeaderField: "Content-Type")
        addAuthHeader(&request)
        var body: [String: Any] = ["kind": kind]
        if let refKind, !refKind.isEmpty { body["ref_kind"] = refKind }
        if let refId, !refId.isEmpty { body["ref_id"] = refId }
        if let payload, !payload.isEmpty { body["payload"] = payload }
        if let sessionId, !sessionId.isEmpty { body["session_id"] = sessionId }
        request.httpBody = try JSONSerialization.data(withJSONObject: body)
        let (_, response) = try await session.data(for: request)
        try validateResponse(response)
    }

    /// Fetch the current learning-progress payload powering the status bar
    /// gauge. Read-only snapshot — recomputed server-side on every call.
    func learningProgress() async throws -> LearningProgressResponse {
        let url = baseURL.appendingPathComponent("insights/learning-progress")
        var request = URLRequest(url: url)
        request.httpMethod = "GET"
        addAuthHeader(&request)
        let (data, response) = try await session.data(for: request)
        try validateResponse(response)
        return try JSONDecoder().decode(LearningProgressResponse.self, from: data)
    }

    // MARK: - Agenda

    /// Fetch the agenda context: upcoming actions and deadlines extracted from
    /// recent knowledge items. The `rangeHours` parameter controls how far back
    /// to look for items (default: 48 hours).
    func agendaContext(rangeHours: Int = 48) async throws -> AgendaContextResponse {
        var components = URLComponents(
            url: baseURL.appendingPathComponent("agenda/context"),
            resolvingAgainstBaseURL: false
        )!
        components.queryItems = [URLQueryItem(name: "range_hours", value: "\(rangeHours)")]

        var request = URLRequest(url: components.url!)
        request.httpMethod = "GET"
        addAuthHeader(&request)

        let (data, response) = try await session.data(for: request)
        try validateResponse(response)
        return try JSONDecoder().decode(AgendaContextResponse.self, from: data)
    }

    // MARK: - Timeline

    /// Build a chaptered timeline for a free-form query. The sidecar runs
    /// the unified searcher, flattens results into dated events, clusters
    /// them into chapters, and titles each chapter via the local LLM.
    func timelineQuery(
        _ query: String,
        focusScope: TimelineFocusScope? = nil,
        rangeDays: Int? = nil,
        topDocs: Int? = nil
    ) async throws -> TimelineResponse {
        let url = baseURL.appendingPathComponent("timeline/query")
        var request = URLRequest(url: url)
        request.httpMethod = "POST"
        request.setValue("application/json", forHTTPHeaderField: "Content-Type")
        addAuthHeader(&request)

        let body = TimelineQueryRequest(
            query: query,
            focusScope: focusScope,
            rangeDays: rangeDays,
            topDocs: topDocs
        )
        request.httpBody = try JSONEncoder().encode(body)

        let (data, response) = try await session.data(for: request)
        try validateResponse(response)
        return try JSONDecoder().decode(TimelineResponse.self, from: data)
    }
}

// MARK: - Request/Response Types

struct HealthResponse: Codable, Sendable {
    let status: String
    let version: String
    let lmStudio: String
    let uptimeSeconds: Int?

    enum CodingKeys: String, CodingKey {
        case status, version
        case lmStudio = "lm_studio"
        case uptimeSeconds = "uptime_seconds"
    }
}

struct ModelsResponse: Codable, Sendable {
    let models: [ModelInfo]
}

struct ModelInfo: Codable, Identifiable, Sendable {
    let id: String
    let name: String
    let ctxWindow: Int?

    enum CodingKeys: String, CodingKey {
        case id, name
        case ctxWindow = "ctx_window"
    }
}

struct ChatMessage: Codable, Sendable {
    let role: String
    let content: String
    /// Non-text payloads (images, audio, document refs). The sidecar
    /// resolves document refs to inline text and translates image/audio
    /// into the runtime-specific multimodal content blocks.
    let attachments: [Attachment]?

    init(role: String, content: String, attachments: [Attachment]? = nil) {
        self.role = role
        self.content = content
        self.attachments = attachments
    }

    /// Create from app Message model
    init(from message: Message) {
        self.role = message.role.rawValue
        self.content = message.content
        self.attachments = message.attachments
    }

    enum CodingKeys: String, CodingKey {
        case role, content, attachments
    }

    func encode(to encoder: Encoder) throws {
        var c = encoder.container(keyedBy: CodingKeys.self)
        try c.encode(role, forKey: .role)
        try c.encode(content, forKey: .content)
        try c.encodeIfPresent(attachments, forKey: .attachments)
    }
}

struct ChatRequest: Codable, Sendable {
    let messages: [ChatMessage]
    let model: String?
    let stream: Bool
    let temperature: Double?
    let maxTokens: Int?
    let recentSourceIDs: [String]?
    let sessionId: String?
    let focusScope: FocusScopePayload?

    enum CodingKeys: String, CodingKey {
        case messages, model, stream, temperature
        case maxTokens = "max_tokens"
        case recentSourceIDs = "recent_source_ids"
        case sessionId = "session_id"
        case focusScope = "focus_scope"
    }
}

/// Mirrors the sidecar's retrieval.FocusScope. When non-nil and non-empty,
/// retrieval is restricted to documents linked to one of the listed projects
/// or carrying one of the listed tags.
struct FocusScopePayload: Codable, Sendable {
    let projectIds: [String]?
    let tagIds: [String]?

    enum CodingKeys: String, CodingKey {
        case projectIds = "project_ids"
        case tagIds = "tag_ids"
    }

    /// Returns nil when both lists are empty so the JSON omits the field
    /// entirely (the server treats nil and empty equivalently as "no focus").
    static func from(projectId: String?, tagIds: [String]) -> FocusScopePayload? {
        let projects = projectId.map { [$0] } ?? []
        if projects.isEmpty && tagIds.isEmpty { return nil }
        return FocusScopePayload(
            projectIds: projects.isEmpty ? nil : projects,
            tagIds: tagIds.isEmpty ? nil : tagIds
        )
    }
}

struct ChatResponse: Codable, Sendable {
    let content: String
}

struct StreamEvent: Codable, Sendable {
    let delta: String?
    let done: Bool
}

// MARK: - Brief

struct BriefRunRequest: Codable, Sendable {
    let projectId: String?
    let lookbackHours: Int?

    enum CodingKeys: String, CodingKey {
        case projectId = "project_id"
        case lookbackHours = "lookback_hours"
    }
}

struct BriefRunResponse: Codable, Sendable {
    /// Always "queued" today — surfaced for parity with future statuses.
    let status: String
    /// Echoes back the project_id when scoped, nil for daily briefs.
    let projectId: String?

    enum CodingKeys: String, CodingKey {
        case status
        case projectId = "project_id"
    }
}

/// SSE error from backend
struct SSEError: Codable, Sendable {
    let code: String
    let message: String
}

/// Extended stream event that can include RAG context or error
struct StreamUsage: Codable, Sendable, Equatable {
    let promptTokens: Int?
    let completionTokens: Int?
    let totalTokens: Int?

    enum CodingKeys: String, CodingKey {
        case promptTokens = "prompt_tokens"
        case completionTokens = "completion_tokens"
        case totalTokens = "total_tokens"
    }
}

struct RAGStreamEvent: Codable, Sendable {
    let type: String?
    let delta: String?
    let done: Bool?
    let sources: [RAGSource]?
    let intent: RAGIntent?
    let error: SSEError?
    let usage: StreamUsage?
}

/// Events yielded by the RAG chat stream
enum ChatStreamEvent: Sendable {
    case ragContext(RAGContext)
    case delta(String)
    case toolCall(ToolCall)
    case done(StreamUsage?)
    case error(String)
}

/// Decode a `tool_call` SSE payload into the domain model. The sidecar
/// emits arguments and result as raw JSON values; we re-serialise them to
/// compact JSON strings so the UI can persist them through Codable without
/// a heterogeneous-JSON decoder. Returns nil when the payload is missing
/// the required identifying fields — the stream handler skips it then.
func decodeToolCallSSE(_ data: Data) -> ToolCall? {
    guard
        let object = try? JSONSerialization.jsonObject(with: data) as? [String: Any],
        let id = object["id"] as? String,
        let name = object["name"] as? String
    else {
        return nil
    }
    let arguments = serializeJSONValue(object["arguments"]) ?? "{}"
    let result = serializeJSONValue(object["result"])
    let errorMessage = object["error"] as? String
    return ToolCall(id: id, name: name, arguments: arguments, result: result, errorMessage: errorMessage)
}

private func serializeJSONValue(_ value: Any?) -> String? {
    guard let value, !(value is NSNull) else { return nil }
    if let str = value as? String { return str }
    if JSONSerialization.isValidJSONObject(value) {
        if let bytes = try? JSONSerialization.data(withJSONObject: value, options: []),
           let s = String(data: bytes, encoding: .utf8) {
            return s
        }
        return nil
    }
    // Wrap scalar numbers/bools so JSONSerialization will encode them.
    let wrapped = ["v": value]
    if let bytes = try? JSONSerialization.data(withJSONObject: wrapped, options: []),
       let s = String(data: bytes, encoding: .utf8),
       let start = s.range(of: ":")?.upperBound,
       let end = s.range(of: "}", options: .backwards)?.lowerBound {
        return String(s[start..<end])
    }
    return nil
}

// MARK: - Errors

enum SidecarError: LocalizedError {
    case invalidResponse
    case httpError(statusCode: Int)
    case connectionFailed
    case unauthorized
    case serviceUnavailable
    case invalidToken
    case keychainError(status: OSStatus)

    var errorDescription: String? {
        switch self {
        case .invalidResponse:
            return "Invalid response from sidecar"
        case .httpError(let statusCode):
            return "HTTP error: \(statusCode)"
        case .connectionFailed:
            return "Failed to connect to sidecar. Make sure it is running."
        case .unauthorized:
            return "Authentication failed. Please check your API token."
        case .serviceUnavailable:
            return "Sidecar service is unavailable. LM Studio may not be running."
        case .invalidToken:
            return "Invalid token format"
        case .keychainError(let status):
            return "Keychain error: \(status)"
        }
    }
}

// MARK: - Sidecar Config Models

struct SidecarConfig: Codable, Sendable {
    struct LMStudio: Codable, Sendable {
        var url: String
        var embeddingUrl: String
        var modelDefault: String
        var embeddingModel: String
        var embeddingMaxTokens: Int
        var timeoutSeconds: Int
        var maxRetries: Int

        enum CodingKeys: String, CodingKey {
            case url
            case embeddingUrl = "embedding_url"
            case modelDefault = "model_default"
            case embeddingModel = "embedding_model"
            case embeddingMaxTokens = "embedding_max_tokens"
            case timeoutSeconds = "timeout_seconds"
            case maxRetries = "max_retries"
        }
    }

    struct Logging: Codable, Sendable {
        var level: String
    }

    struct DailyBrief: Codable, Sendable {
        var enabled: Bool
        var hourLocal: String
        var maxItems: Int
        var lookbackHours: Int

        enum CodingKeys: String, CodingKey {
            case enabled
            case hourLocal = "hour_local"
            case maxItems = "max_items"
            case lookbackHours = "lookback_hours"
        }
    }

    struct Retrieval: Codable, Sendable {
        var useLlmIntent: Bool
        var useJudge: Bool
        var temporalScoringMode: String
        var entitySearchFallback: Bool
        var entitySearchMinScore: Double

        enum CodingKeys: String, CodingKey {
            case useLlmIntent = "use_llm_intent"
            case useJudge = "use_judge"
            case temporalScoringMode = "temporal_scoring_mode"
            case entitySearchFallback = "entity_search_fallback"
            case entitySearchMinScore = "entity_search_min_score"
        }
    }

    var lmStudio: LMStudio
    var logging: Logging
    var dailyBrief: DailyBrief
    var retrieval: Retrieval

    enum CodingKeys: String, CodingKey {
        case lmStudio = "lm_studio"
        case logging
        case dailyBrief = "daily_brief"
        case retrieval
    }
}

/// Partial update — only non-nil fields are sent to PATCH /config.
struct SidecarConfigPatch: Codable, Sendable {
    struct LMStudio: Codable, Sendable {
        var url: String?
        var embeddingUrl: String?
        var modelDefault: String?
        var embeddingModel: String?
        var embeddingMaxTokens: Int?
        var timeoutSeconds: Int?
        var maxRetries: Int?

        enum CodingKeys: String, CodingKey {
            case url
            case embeddingUrl = "embedding_url"
            case modelDefault = "model_default"
            case embeddingModel = "embedding_model"
            case embeddingMaxTokens = "embedding_max_tokens"
            case timeoutSeconds = "timeout_seconds"
            case maxRetries = "max_retries"
        }
    }

    struct Logging: Codable, Sendable {
        var level: String?
    }

    struct DailyBrief: Codable, Sendable {
        var enabled: Bool?
        var hourLocal: String?
        var maxItems: Int?
        var lookbackHours: Int?

        enum CodingKeys: String, CodingKey {
            case enabled
            case hourLocal = "hour_local"
            case maxItems = "max_items"
            case lookbackHours = "lookback_hours"
        }
    }

    struct Retrieval: Codable, Sendable {
        var useLlmIntent: Bool?
        var useJudge: Bool?
        var temporalScoringMode: String?
        var entitySearchFallback: Bool?

        enum CodingKeys: String, CodingKey {
            case useLlmIntent = "use_llm_intent"
            case useJudge = "use_judge"
            case temporalScoringMode = "temporal_scoring_mode"
            case entitySearchFallback = "entity_search_fallback"
        }
    }

    var lmStudio: LMStudio?
    var logging: Logging?
    var dailyBrief: DailyBrief?
    var retrieval: Retrieval?

    enum CodingKeys: String, CodingKey {
        case lmStudio = "lm_studio"
        case logging
        case dailyBrief = "daily_brief"
        case retrieval
    }
}

private extension String {
    /// Percent-encodes a value for inclusion in a URL path component.
    /// Email addresses (the canonical mail account_id) often contain "+" or
    /// other characters that must be escaped to survive routing intact.
    var urlPathEscaped: String {
        addingPercentEncoding(withAllowedCharacters: .urlPathAllowed) ?? self
    }
}
