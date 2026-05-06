import Foundation

enum OnboardingStep: Int, CaseIterable, Identifiable {
    case welcome
    case connectModel
    case connectMail
    case importFolder
    case ready

    var id: Int { rawValue }

    var title: String {
        switch self {
        case .welcome:       return "Welcome"
        case .connectModel:  return "Connect AI model"
        case .connectMail:   return "Connect mail"
        case .importFolder:  return "Import documents"
        case .ready:         return "All set"
        }
    }

    /// True when the step is optional and a "Skip" button should be offered.
    /// Welcome (entry) and Ready (recap) are always non-skippable.
    var isSkippable: Bool {
        switch self {
        case .welcome, .ready:                       return false
        case .connectModel:                          return true
        case .connectMail, .importFolder:            return true
        }
    }

    /// True when the step view renders its own primary CTA (e.g. "Test &
    /// continue") and the shared footer should hide its generic "Continue"
    /// button to avoid two competing primary actions.
    var ownsPrimaryAction: Bool {
        switch self {
        case .connectModel: return true
        default:            return false
        }
    }

    var next: OnboardingStep? {
        OnboardingStep(rawValue: rawValue + 1)
    }

    var previous: OnboardingStep? {
        guard rawValue > 0 else { return nil }
        return OnboardingStep(rawValue: rawValue - 1)
    }
}
