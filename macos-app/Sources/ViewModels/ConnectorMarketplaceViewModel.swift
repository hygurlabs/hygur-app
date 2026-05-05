import Foundation
import SwiftUI

@MainActor
@Observable
final class ConnectorMarketplaceViewModel {
    var listings: [MarketplaceListing] = []
    var isLoading = false
    var error: String?
    var selectedCategory: String? = nil
    var installingID: String? = nil

    private let service: SidecarService

    init(service: SidecarService = .fromSettings()) {
        self.service = service
    }

    // MARK: - Derived

    var categories: [String] {
        var seen = Set<String>()
        var result: [String] = []
        for listing in listings {
            for cat in listing.categories where !seen.contains(cat) {
                seen.insert(cat)
                result.append(cat)
            }
        }
        return result.sorted()
    }

    var filteredListings: [MarketplaceListing] {
        guard let cat = selectedCategory else { return listings }
        return listings.filter { $0.categories.contains(cat) }
    }

    // MARK: - Actions

    func load() async {
        isLoading = true
        error = nil
        defer { isLoading = false }
        do {
            listings = try await service.listMarketplaceConnectors()
        } catch {
            self.error = error.localizedDescription
        }
    }

    func install(typeID: String) async {
        installingID = typeID
        defer { installingID = nil }
        do {
            try await service.installMarketplaceConnector(typeID: typeID)
            await load()
        } catch {
            self.error = error.localizedDescription
        }
    }

    func clearError() { error = nil }
}
