import Foundation

/// Read-only client for the public GitHub Releases API. No auth needed since
/// hygurlabs/hygur-app is public; rate limit (60/h unauthenticated) is far
/// more than this app will ever consume.
actor GitHubReleasesClient {
    enum ClientError: LocalizedError, Equatable {
        case noStableRelease
        case rateLimited
        case http(Int)
        case decoding
        case network(String)

        var errorDescription: String? {
            switch self {
            case .noStableRelease:
                return "Aucune version stable n'est encore publiée."
            case .rateLimited:
                return "Trop de requêtes vers GitHub. Réessayez dans une heure."
            case .http(let code):
                return "GitHub a répondu avec un code \(code)."
            case .decoding:
                return "Réponse GitHub invalide."
            case .network(let message):
                return message
            }
        }
    }

    private let session: URLSession
    private let owner: String
    private let repo: String
    private let decoder: JSONDecoder

    init(owner: String = "hygurlabs", repo: String = "hygur-app") {
        let config = URLSessionConfiguration.default
        config.timeoutIntervalForRequest = 15
        config.timeoutIntervalForResource = 30
        config.waitsForConnectivity = false
        self.session = URLSession(configuration: config)
        self.owner = owner
        self.repo = repo

        let decoder = JSONDecoder()
        decoder.dateDecodingStrategy = .iso8601
        self.decoder = decoder
    }

    func fetchLatestRelease() async throws -> ReleaseInfo {
        let url = URL(string: "https://api.github.com/repos/\(owner)/\(repo)/releases/latest")!
        var request = URLRequest(url: url)
        request.setValue("application/vnd.github+json", forHTTPHeaderField: "Accept")
        request.setValue("2022-11-28", forHTTPHeaderField: "X-GitHub-Api-Version")
        request.setValue("Hygur/\(Bundle.main.appVersion)", forHTTPHeaderField: "User-Agent")

        let data: Data
        let response: URLResponse
        do {
            (data, response) = try await session.data(for: request)
        } catch {
            throw ClientError.network(error.localizedDescription)
        }

        guard let http = response as? HTTPURLResponse else {
            throw ClientError.network("Réponse réseau invalide.")
        }

        switch http.statusCode {
        case 200:
            do {
                return try decoder.decode(ReleaseInfo.self, from: data)
            } catch {
                throw ClientError.decoding
            }
        case 404:
            // No public stable release yet — perfectly normal for a brand-new repo.
            throw ClientError.noStableRelease
        case 403, 429:
            throw ClientError.rateLimited
        default:
            throw ClientError.http(http.statusCode)
        }
    }
}
