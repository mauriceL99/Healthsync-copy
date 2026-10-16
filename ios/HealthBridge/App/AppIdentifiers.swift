import Foundation

/// Identifiers derived from the bundle identifier. Renaming the app then only means
/// changing HB_BUNDLE_ID_PREFIX (setup.sh / Configuration/Local.xcconfig); Info.plist
/// expands the same values for the background task identifiers, and App Store Connect
/// products must follow it.
enum AppIdentifiers {
    /// The placeholder is only a fallback for contexts without a main bundle identifier.
    static let base = Bundle.main.bundleIdentifier ?? "com.example.healthbridge"

    static let refreshTask = base + ".refresh"
    static let backfillTask = base + ".backfill"
    static let proLifetimeProduct = base + ".pro.lifetime"
    static let proYearlyProduct = base + ".pro.yearly"
}
