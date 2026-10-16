import StoreKit
import SwiftUI

struct SettingsView: View {
    @EnvironmentObject var model: AppModel
    @EnvironmentObject var purchases: PurchaseManager
    @State private var showPaywall = false
    @State private var confirmUnpair = false
    @State private var confirmResend = false
    @State private var isUnpairing = false
    @State private var unpairAlert: UnpairAlert?
    @State private var showManageSubscriptions = false

    var body: some View {
        Form {
            agentSection
            syncSection
            if AppEdition.sellsPro {
                proSection
            }
            privacySection
            aboutSection
            unpairSection
        }
        .navigationTitle("Settings")
        .navigationBarTitleDisplayMode(.inline)
        .paywallSheet(isPresented: $showPaywall)
        .manageSubscriptionsSheet(isPresented: $showManageSubscriptions)
        .confirmationDialog(
            "Unpair this iPhone?",
            isPresented: $confirmUnpair,
            titleVisibility: .visible
        ) {
            Button("Unpair", role: .destructive) {
                Task { await unpair() }
            }
        } message: {
            Text("The relay unlinks this iPhone and all keys are deleted from this device. Data your agent already received stays with your agent.")
        }
        .confirmationDialog(
            "Send the full history again?",
            isPresented: $confirmResend,
            titleVisibility: .visible
        ) {
            Button("Send \(model.effectiveHistoryDays) days") {
                Task { await model.syncNow(trigger: .manual, forceBackfill: true) }
            }
        } message: {
            Text("Useful after enabling new categories. Your agent merges the data per day.")
        }
        .alert(unpairAlert?.title ?? "", isPresented: unpairAlertBinding, presenting: unpairAlert) { _ in
            Button("OK", role: .cancel) {}
        } message: { alert in
            Text(alert.message)
        }
    }

    // MARK: Sections

    private var agentSection: some View {
        Section {
            if let pairing = model.pairing {
                InfoRow(label: "Agent", value: model.remoteStatus?.agentName.map { HB1.cleanLabel($0) } ?? pairing.displayName)
                InfoRow(label: "Relay", value: pairing.relayHost)
                InfoRow(label: "Safety code", value: pairing.safetyCode, monospaced: true)
                InfoRow(label: "Key ID", value: pairing.keyID, monospaced: true)
                InfoRow(label: "Device ID", value: pairing.deviceID, monospaced: true)
                InfoRow(label: "Linked", value: Formatting.dateTime(pairing.linkedAt))
            }
        } header: {
            Text("Paired agent")
        } footer: {
            Text("The safety code must match the one your agent shows on your computer. If it ever differs, unpair and pair again.")
        }
    }

    private var syncSection: some View {
        Section {
            Picker("History window", selection: historyBinding) {
                ForEach(HistoryWindow.allCases) { window in
                    Text(window.requiresPro && !purchases.isPro ? "\(window.title) (Pro)" : window.title)
                        .tag(window)
                }
            }
            NavigationLink("Metrics") {
                MetricsView()
            }
            NavigationLink("Activity log") {
                ActivityLogView()
            }
            Button("Send full history again") {
                confirmResend = true
            }
            .disabled(model.isSyncing)
        } header: {
            Text("Sync")
        } footer: {
            Text("Each sync re-sends the last \(SyncPlan.incrementalDays) days. Larger history imports are split into envelopes of up to \(SyncPlan.chunkDays) days.")
        }
    }

    private var proSection: some View {
        Section {
            if purchases.isPro {
                Label("HealthBridge Pro is active", systemImage: "checkmark.seal.fill")
                    .foregroundStyle(.green)
                Button("Manage subscription") {
                    showManageSubscriptions = true
                }
            } else {
                Button {
                    showPaywall = true
                } label: {
                    Label("Upgrade to HealthBridge Pro", systemImage: "sparkles")
                }
            }
            Button("Restore purchases") {
                Task { await purchases.restore() }
            }
        } header: {
            Text("HealthBridge Pro")
        } footer: {
            Text("Free: manual sync and the last \(HistoryWindow.freeLimit.title). Pro: background sync, up to \(HistoryWindow.proLimit.title) and workout heart-rate details.")
        }
    }

    private var privacySection: some View {
        Section {
            VStack(alignment: .leading, spacing: 10) {
                FeatureRow(
                    symbol: "iphone",
                    title: "Encrypted on this iPhone",
                    text: "Each upload is encrypted with a fresh key (X25519 + ChaCha20-Poly1305) for your agent's public key."
                )
                FeatureRow(
                    symbol: "server.rack",
                    title: "Relay sees ciphertext only",
                    text: "The relay stores encrypted envelopes until your agent fetches them, then deletes them."
                )
                FeatureRow(
                    symbol: "hand.raised.fill",
                    title: "No ads, no analytics",
                    text: "Health data is never used for advertising, never sold and never logged."
                )
            }
            .padding(.vertical, 6)
        } header: {
            Text("Privacy")
        }
    }

    private var aboutSection: some View {
        Section {
            InfoRow(label: "Version", value: "\(AppInfo.version) (\(AppInfo.build))")
            DisclaimerCard()
                .listRowInsets(EdgeInsets(top: 8, leading: 8, bottom: 8, trailing: 8))
        } header: {
            Text("About")
        }
    }

    private var unpairSection: some View {
        Section {
            Button(role: .destructive) {
                confirmUnpair = true
            } label: {
                HStack {
                    if isUnpairing {
                        ProgressView()
                    }
                    Text(isUnpairing ? "Unpairing…" : "Unpair this iPhone")
                }
            }
            .disabled(isUnpairing || model.isSyncing)
        }
    }

    // MARK: Bindings & actions

    private var historyBinding: Binding<HistoryWindow> {
        Binding(
            get: { model.settings.effectiveHistoryWindow(isPro: purchases.isPro) },
            set: { window in
                if window.requiresPro && !purchases.isPro {
                    showPaywall = true
                } else {
                    model.updateSettings { $0.historyWindow = window }
                }
            }
        )
    }

    private var unpairAlertBinding: Binding<Bool> {
        Binding(
            get: { unpairAlert != nil },
            set: { if !$0 { unpairAlert = nil } }
        )
    }

    @MainActor
    private func unpair() async {
        isUnpairing = true
        let result = await model.unpair()
        isUnpairing = false
        if let localError = result.localError {
            let relay = result.reachedRelay ? "The relay unlinked this iPhone, but the" : "The"
            unpairAlert = UnpairAlert(
                title: "Unpairing failed",
                message: "\(relay) keys on this iPhone could not be deleted: \(localError) Try again."
            )
        } else if !result.reachedRelay {
            unpairAlert = UnpairAlert(
                title: "Unpaired",
                message: "The relay could not be reached. Local keys were deleted; unlink the device on your agent as well."
            )
        }
    }
}

private struct UnpairAlert {
    let title: String
    let message: String
}
