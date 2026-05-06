import Foundation

/// Minimal projection of a GitHub Releases API response — only the fields the
/// updater actually consumes. The API returns much more; we ignore it.
struct ReleaseInfo: Sendable, Codable, Equatable {
    let tagName: String
    let name: String
    let body: String
    let htmlURL: URL
    let publishedAt: Date
    let assets: [ReleaseAsset]

    /// Tag without the leading "v" (e.g. "0.1.2"). GitHub tags follow the
    /// "v0.1.2" convention but we compare against the bundle's plain semver.
    var version: String {
        tagName.hasPrefix("v") ? String(tagName.dropFirst()) : tagName
    }

    /// The DMG asset, when present. Releases without a `.dmg` (e.g. notes-only
    /// drafts that slipped through) cannot be auto-installed.
    var dmgAsset: ReleaseAsset? {
        assets.first { $0.name.lowercased().hasSuffix(".dmg") }
    }

    enum CodingKeys: String, CodingKey {
        case tagName = "tag_name"
        case name
        case body
        case htmlURL = "html_url"
        case publishedAt = "published_at"
        case assets
    }
}

struct ReleaseAsset: Sendable, Codable, Equatable {
    let name: String
    let size: Int64
    let browserDownloadURL: URL
    /// Format: "sha256:<hex>". Populated by GitHub for assets uploaded via the
    /// modern API (gh CLI, releases API). Older uploads may return nil.
    let digest: String?

    /// Extract the lowercase hex sha256 digest, if present and well-formed.
    var sha256Hex: String? {
        guard let digest, digest.hasPrefix("sha256:") else { return nil }
        let hex = String(digest.dropFirst("sha256:".count)).lowercased()
        return hex.count == 64 ? hex : nil
    }

    enum CodingKeys: String, CodingKey {
        case name
        case size
        case browserDownloadURL = "browser_download_url"
        case digest
    }
}
