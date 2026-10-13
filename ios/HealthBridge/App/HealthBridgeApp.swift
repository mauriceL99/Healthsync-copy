import SwiftUI
import UIKit

@main
struct HealthBridgeApp: App {
    @UIApplicationDelegateAdaptor(AppDelegate.self) private var appDelegate
    @StateObject private var model = AppModel()
    @Environment(\.scenePhase) private var scenePhase

    var body: some Scene {
        WindowGroup {
            RootView()
                .environmentObject(model)
                .environmentObject(model.purchases)
        }
        .onChange(of: scenePhase) { _, newPhase in
            switch newPhase {
            case .active:
                Task { await model.didBecomeActive() }
            case .background:
                model.didEnterBackground()
            default:
                break
            }
        }
    }
}

/// BGTask handlers must be registered before launch finishes, and HealthKit observer
/// queries for background delivery must be set up at launch as well.
final class AppDelegate: NSObject, UIApplicationDelegate {
    func application(
        _ application: UIApplication,
        didFinishLaunchingWithOptions launchOptions: [UIApplication.LaunchOptionsKey: Any]? = nil
    ) -> Bool {
        SettingsStore.prepareFirstLaunchIfNeeded()
        BackgroundSync.shared.registerTasks()
        BackgroundSync.shared.applyConfiguration()
        return true
    }
}
