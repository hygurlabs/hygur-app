import Foundation

/// Lightweight Sublime-style fuzzy matcher used by the command palette.
///
/// Scoring rules (kept simple — palette inputs are short):
///   * All query characters must appear in `candidate` in order (case-insensitive).
///   * Match at the start of the candidate gets a base bonus.
///   * Match at the start of a word (after a space, dash, or underscore) gets a bonus.
///   * Consecutive matches compound — typing the prefix of a word always wins.
///   * Exact substring match gets a strong constant boost so "chat" beats "chxxxat".
///
/// Returns `nil` when the query cannot be aligned in `candidate`. Higher score = better match.
enum FuzzyMatcher {
    static func score(query: String, candidate: String) -> Int? {
        let trimmed = query.trimmingCharacters(in: .whitespaces)
        if trimmed.isEmpty {
            return 0
        }
        let q = Array(trimmed.lowercased())
        let c = Array(candidate.lowercased())
        guard !c.isEmpty else { return nil }

        var score = 0
        var qi = 0
        var lastMatchIdx = -2 // ensures the first match doesn't count as consecutive
        var matchedAtStart = false

        for (ci, char) in c.enumerated() {
            if qi >= q.count { break }
            if char != q[qi] { continue }

            // Position bonuses.
            if ci == 0 {
                score += 8
                matchedAtStart = true
            } else {
                let prev = c[ci - 1]
                if prev == " " || prev == "-" || prev == "_" || prev == "/" {
                    score += 6
                }
            }
            // Consecutive-match streak bonus.
            if ci == lastMatchIdx + 1 {
                score += 4
            }
            score += 1
            lastMatchIdx = ci
            qi += 1
        }

        guard qi == q.count else { return nil }

        // Substring bonus: the candidate contains the query verbatim.
        if let _ = candidate.range(of: trimmed, options: [.caseInsensitive]) {
            score += 12
        }
        // Prefer shorter candidates when scores tie — typing "ch" should rank
        // "Chat" above "Chat Sessions Recents".
        score -= max(0, c.count - q.count) / 8

        if matchedAtStart {
            score += 2
        }
        return score
    }

    /// Returns indices into `items` that match `query`, sorted by descending score.
    /// Empty query returns all indices in original order.
    static func rank<T>(items: [T], query: String, key: (T) -> String) -> [T] {
        let trimmed = query.trimmingCharacters(in: .whitespaces)
        if trimmed.isEmpty { return items }
        return items
            .compactMap { item -> (T, Int)? in
                guard let s = score(query: trimmed, candidate: key(item)) else { return nil }
                return (item, s)
            }
            .sorted { $0.1 > $1.1 }
            .map { $0.0 }
    }
}
