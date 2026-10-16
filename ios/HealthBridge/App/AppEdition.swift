import Foundation

/// Which build this is.
///
/// The personal edition (default) is meant for your own iPhone and friends: every feature
/// is unlocked and the app never talks to the App Store. The store edition sells
/// "HealthBridge Pro" through StoreKit; build it with the Swift flag `STORE_EDITION`
/// (see ios/README.md, "Store-Edition").
enum AppEdition {
    #if STORE_EDITION
    static let sellsPro = true
    #else
    static let sellsPro = false
    #endif
}
