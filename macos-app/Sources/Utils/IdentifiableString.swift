/// Thin wrapper that makes a `String` conform to `Identifiable`,
/// enabling its use with `.sheet(item:)` and similar SwiftUI APIs.
struct IdentifiableString: Identifiable, Hashable {
    let value: String
    var id: String { value }

    init(_ value: String) {
        self.value = value
    }
}
