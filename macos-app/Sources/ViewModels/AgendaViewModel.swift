import Foundation
import Observation

@MainActor
@Observable
final class AgendaViewModel {
    var actions: [AgendaAction] = []
    var isLoading = false
    var error: String?

    private let service: SidecarService

    init(service: SidecarService = .fromSettings()) {
        self.service = service
    }

    func refresh() async {
        isLoading = true
        defer { isLoading = false }
        do {
            let resp = try await service.agendaContext()
            actions = resp.actions
        } catch {
            self.error = error.localizedDescription
        }
    }
}
