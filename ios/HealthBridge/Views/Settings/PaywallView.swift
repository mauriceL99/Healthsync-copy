import StoreKit
import SwiftUI

struct PaywallView: View {
    @EnvironmentObject var purchases: PurchaseManager
    @Environment(\.dismiss) private var dismiss

    var body: some View {
        NavigationStack {
            ScrollView {
                VStack(spacing: 24) {
                    VStack(spacing: 10) {
                        Image(systemName: "sparkles")
                            .font(.system(size: 52, weight: .semibold))
                            .foregroundStyle(.orange)
                            .symbolEffect(.pulse)
                        Text("HealthBridge Pro")
                            .font(.largeTitle.bold())
                        Text("Let your agent stay up to date without opening the app.")
                            .font(.body)
                            .foregroundStyle(.secondary)
                            .multilineTextAlignment(.center)
                    }
                    .padding(.top, 16)

                    VStack(alignment: .leading, spacing: 16) {
                        FeatureRow(
                            symbol: "clock.arrow.2.circlepath",
                            title: "Scheduled sync",
                            text: "Every \(SyncInterval.choicesText) in the background."
                        )
                        FeatureRow(
                            symbol: "bolt.heart.fill",
                            title: "Sync on new Health data",
                            text: "Apple Health wakes HealthBridge when new data arrives."
                        )
                        FeatureRow(
                            symbol: "calendar",
                            title: "Up to \(HistoryWindow.proLimit.title) of history",
                            text: "Import up to \(HistoryWindow.proLimit.title) instead of the last \(HistoryWindow.freeLimit.title)."
                        )
                        FeatureRow(
                            symbol: "waveform.path.ecg",
                            title: "Workout heart-rate details",
                            text: "Zones, a 5-second heart-rate curve and 1-minute recovery for every workout."
                        )
                    }

                    if purchases.isPro {
                        Label("Pro is active. Thank you!", systemImage: "checkmark.seal.fill")
                            .font(.headline)
                            .foregroundStyle(.green)
                    } else {
                        productButtons
                    }

                    if let error = purchases.lastError {
                        Text(error)
                            .font(.footnote)
                            .foregroundStyle(.red)
                            .multilineTextAlignment(.center)
                    }

                    Button("Restore purchases") {
                        Task { await purchases.restore() }
                    }
                    .font(.subheadline)

                    Text("The yearly subscription renews automatically unless cancelled at least 24 hours before the end of the period. Manage it in your App Store account settings.")
                        .font(.caption2)
                        .foregroundStyle(.secondary)
                        .multilineTextAlignment(.center)
                }
                .padding(24)
            }
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Close") { dismiss() }
                }
            }
            .task {
                await purchases.loadProducts()
            }
            .onChange(of: purchases.isPro) { _, isPro in
                if isPro { dismiss() }
            }
        }
    }

    @ViewBuilder
    private var productButtons: some View {
        if purchases.products.isEmpty {
            if purchases.isLoadingProducts {
                ProgressView()
            } else {
                Text("Products are not available right now.")
                    .font(.subheadline)
                    .foregroundStyle(.secondary)
            }
        } else {
            VStack(spacing: 12) {
                if let yearly = purchases.yearlyProduct {
                    productButton(yearly, caption: yearlyCaption(yearly), prominent: true)
                }
                if let lifetime = purchases.lifetimeProduct {
                    productButton(lifetime, caption: "One-time purchase", prominent: false)
                }
            }
        }
    }

    private func yearlyCaption(_ product: Product) -> String {
        if let offer = product.subscription?.introductoryOffer, offer.paymentMode == .freeTrial {
            return "Free trial, then \(product.displayPrice) per year"
        }
        return "\(product.displayPrice) per year"
    }

    @ViewBuilder
    private func productButton(_ product: Product, caption: String, prominent: Bool) -> some View {
        let label = VStack(spacing: 2) {
            HStack {
                Text(product.displayName)
                    .font(.headline)
                if purchases.purchaseInFlight == product.id {
                    ProgressView()
                }
            }
            Text(caption)
                .font(.caption)
        }
        .frame(maxWidth: .infinity)
        .padding(.vertical, 4)

        if prominent {
            Button {
                Task { await purchases.purchase(product) }
            } label: {
                label
            }
            .primaryActionStyle()
            .disabled(purchases.purchaseInFlight != nil)
        } else {
            Button {
                Task { await purchases.purchase(product) }
            } label: {
                label
            }
            .secondaryActionStyle()
            .disabled(purchases.purchaseInFlight != nil)
        }
    }
}

/// Presents the paywall with the purchase manager injected explicitly.
struct PaywallSheetModifier: ViewModifier {
    @EnvironmentObject var purchases: PurchaseManager
    @Binding var isPresented: Bool

    func body(content: Content) -> some View {
        content.sheet(isPresented: $isPresented) {
            PaywallView()
                .environmentObject(purchases)
        }
    }
}

extension View {
    func paywallSheet(isPresented: Binding<Bool>) -> some View {
        modifier(PaywallSheetModifier(isPresented: isPresented))
    }
}
