import Foundation
import SwiftUI

@Observable
final class AppSettings {
    var apiKey: String = ""
    var sidecarPort: Int = 8080
    var theme: Theme = .system

    enum Theme: String, CaseIterable {
        case light
        case dark
        case system

        var displayName: String {
            switch self {
            case .light: return "Light"
            case .dark: return "Dark"
            case .system: return "System"
            }
        }
    }
}
