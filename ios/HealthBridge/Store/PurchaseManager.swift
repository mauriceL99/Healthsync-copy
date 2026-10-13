import Combine
import Foundation
import StoreKit

enum PurchaseError: LocalizedError {
    case failedVerification

    var errorDescription: String? {
        switch self {
        case .failedVerification:
            return "The App Store could not verify the purchase."
        }
    }
}

/// StoreKit 2 purchases for "HealthBridge Pro".
///
/// Free: manual sync and `HistoryWindow.freeLimit` of history.
/// Pro: scheduled and on-new-data background sync, up to `HistoryWindow.proLimit` of history
/// and workout heart-rate details.
@MainActor
final class PurchaseManager: ObservableObject {
    nonisolated static let lifetimeProductID = AppIdentifiers.proLifetimeProduct
    nonisolated static let yearlyProductID = AppIdentifiers.proYearlyProduct
    nonisolated static let productIDs: Set<String> = [lifetimeProductID, yearlyProductID]

    @Published private(set) var products: [Product] = []
    @Published private(set) var isPro: Bool
    @Published private(set) var isLoadingProducts = false
    @Published private(set) var purchaseInFlight: String?
    @Published var lastError: String?

    private var updatesTask: Task<Void, Never>?

    init() {
        isPro = EntitlementCache.isPro
        updatesTask = Task.detached(priority: .background) { [weak self] in
            for await result in Transaction.updates {
                if case .verified(let transaction) = result {
                    await transaction.finish()
                }
                await self?.refreshEntitlements()
            }
        }
        Task {
            await refreshEntitlements()
            await loadProducts()
        }
    }

    var lifetimeProduct: Product? {
        products.first { $0.id == Self.lifetimeProductID }
    }

    var yearlyProduct: Product? {
        products.first { $0.id == Self.yearlyProductID }
    }

    func loadProducts() async {
        guard products.isEmpty, !isLoadingProducts else { return }
        isLoadingProducts = true
        defer { isLoadingProducts = false }
        do {
            let loaded = try await Product.products(for: Self.productIDs)
            products = loaded.sorted { $0.price < $1.price }
        } catch {
            lastError = "Could not load products from the App Store: \(error.localizedDescription)"
        }
    }

    /// Returns true when the purchase completed and Pro is unlocked.
    @discardableResult
    func purchase(_ product: Product) async -> Bool {
        purchaseInFlight = product.id
        defer { purchaseInFlight = nil }
        do {
            let result = try await product.purchase()
            switch result {
            case .success(let verification):
                let transaction = try Self.checkVerified(verification)
                await transaction.finish()
                await refreshEntitlements()
                return isPro
            case .userCancelled:
                return false
            case .pending:
                lastError = "The purchase is pending approval."
                return false
            @unknown default:
                return false
            }
        } catch {
            lastError = "The purchase failed: \(error.localizedDescription)"
            return false
        }
    }

    func restore() async {
        do {
            try await AppStore.sync()
        } catch {
            lastError = "Restoring purchases failed (\(error.localizedDescription)). Check that you are signed in to the App Store."
        }
        await refreshEntitlements()
    }

    func refreshEntitlements() async {
        let pro = await Self.verifiedProEntitlement()
        isPro = pro
        EntitlementCache.isPro = pro
    }

    /// Checks `Transaction.currentEntitlements`; usable from background tasks.
    nonisolated static func verifiedProEntitlement() async -> Bool {
        for await result in Transaction.currentEntitlements {
            guard case .verified(let transaction) = result,
                  productIDs.contains(transaction.productID),
                  transaction.revocationDate == nil else { continue }
            if let expiration = transaction.expirationDate, expiration < Date() {
                continue
            }
            return true
        }
        return false
    }

    private static func checkVerified<T>(_ result: VerificationResult<T>) throws -> T {
        switch result {
        case .unverified:
            throw PurchaseError.failedVerification
        case .verified(let value):
            return value
        }
    }
}
