import SwiftUI

struct HomeView: View {
    @EnvironmentObject var model: AppModel
    @EnvironmentObject var purchases: PurchaseManager
    @State private var showPaywall = false

    var body: some View {
        NavigationStack {
            ScrollView {
                VStack(spacing: 16) {
                    StatusCard()
                    SyncNowCard()
                    if model.needsHealthAuthorization {
                        HealthAccessBanner()
                    }
                    SyncModeCard(showPaywall: $showPaywall)
                    NavigationTiles()
                    if !purchases.isPro {
                        ProTeaser(showPaywall: $showPaywall)
                    }
                }
                .padding(16)
            }
            .background(Color(.systemGroupedBackground))
            .navigationTitle("HealthBridge")
            .toolbar {
                ToolbarItem(placement: .topBarTrailing) {
                    NavigationLink {
                        SettingsView()
                    } label: {
                        Image(systemName: "gearshape")
                            .accessibilityLabel("Settings")
                    }
                }
            }
            .refreshable {
                await model.refreshRemoteStatus()
            }
            .paywallSheet(isPresented: $showPaywall)
        }
    }
}

// MARK: - Status

private struct StatusCard: View {
    @EnvironmentObject var model: AppModel

    var body: some View {
        Card {
            HStack(alignment: .center, spacing: 12) {
                ZStack {
                    Circle()
                        .fill(Color.accentColor.opacity(0.15))
                        .frame(width: 44, height: 44)
                    Image(systemName: "link")
                        .foregroundStyle(.tint)
                        .font(.headline)
                }
                VStack(alignment: .leading, spacing: 2) {
                    Text(model.remoteStatus?.agentName.map { HB1.cleanLabel($0) } ?? model.pairing?.displayName ?? "Agent")
                        .font(.headline)
                    Text(model.pairing?.relayHost ?? "")
                        .font(.caption)
                        .foregroundStyle(.secondary)
                        .lineLimit(1)
                        .truncationMode(.middle)
                }
                Spacer()
                Label("Encrypted", systemImage: "lock.fill")
                    .font(.caption.weight(.semibold))
                    .foregroundStyle(.green)
                    .labelStyle(.titleAndIcon)
            }
            Divider()
            InfoRow(label: "Last sync", value: lastSyncText, symbol: "clock")
            InfoRow(label: "Next sync", value: nextSyncText, symbol: "calendar.badge.clock")
            InfoRow(label: "Waiting for agent", value: pendingText, symbol: "tray.full")
            if let error = model.syncState.lastError, !model.isSyncing {
                Label(error, systemImage: "exclamationmark.triangle.fill")
                    .font(.footnote)
                    .foregroundStyle(.orange)
            }
            if let problem = model.syncState.backgroundDeliveryProblem, model.effectiveMode == .onNewData {
                Label(problem, systemImage: "exclamationmark.triangle.fill")
                    .font(.footnote)
                    .foregroundStyle(.orange)
            }
        }
    }

    private var lastSyncText: String {
        guard let date = model.syncState.lastSuccessfulSync else { return "Never" }
        return Formatting.relative(date)
    }

    private var nextSyncText: String {
        switch model.effectiveMode {
        case .manual:
            return "Manual"
        case .scheduled, .onNewData:
            if let date = model.syncState.nextScheduledSync {
                return date > Date() ? "not before \(date.formatted(date: .omitted, time: .shortened))" : "when iOS allows"
            }
            return model.effectiveMode == .onNewData ? "On new Health data" : "Background refresh off"
        }
    }

    private var pendingText: String {
        guard let pending = model.remoteStatus?.pendingEnvelopes else { return "–" }
        return pending == 1 ? "1 envelope" : "\(pending) envelopes"
    }
}

// MARK: - Sync now

private struct SyncNowCard: View {
    @EnvironmentObject var model: AppModel

    var body: some View {
        Card {
            Button {
                Task { await model.syncNow() }
            } label: {
                HStack(spacing: 10) {
                    if model.isSyncing {
                        ProgressView()
                            .tint(.white)
                    } else {
                        Image(systemName: "arrow.triangle.2.circlepath")
                    }
                    Text(model.isSyncing ? "Syncing…" : "Sync now")
                }
                .font(.title3.weight(.semibold))
                .frame(maxWidth: .infinity)
                .padding(.vertical, 6)
            }
            .primaryActionStyle()
            .disabled(model.isSyncing)
            .sensoryFeedback(.success, trigger: lastSuccessDate)

            statusLine
        }
    }

    /// Changes with every successful sync, which triggers the success haptic.
    private var lastSuccessDate: Date? {
        if case .succeeded(let report) = model.status {
            return report.finishedAt
        }
        return nil
    }

    @ViewBuilder
    private var statusLine: some View {
        switch model.status {
        case .idle:
            Text(model.backfillPending
                 ? "The next sync imports your history (\(model.effectiveHistoryDays) days)."
                 : "Sends the last days again; your agent merges them per day.")
                .font(.footnote)
                .foregroundStyle(.secondary)
        case .running(let progress):
            VStack(alignment: .leading, spacing: 6) {
                ProgressView(value: progress.fraction)
                Text(progress.message)
                    .font(.footnote)
                    .foregroundStyle(.secondary)
            }
        case .succeeded(let report):
            Label(
                "Sent \(report.envelopeCount) encrypted \(report.envelopeCount == 1 ? "envelope" : "envelopes") (\(Formatting.bytes(report.ciphertextBytes)))",
                systemImage: "checkmark.circle.fill"
            )
            .font(.footnote)
            .foregroundStyle(.green)
        case .failed(let message):
            Label(message, systemImage: "exclamationmark.triangle.fill")
                .font(.footnote)
                .foregroundStyle(.red)
        }
    }
}

// MARK: - Health access

private struct HealthAccessBanner: View {
    @EnvironmentObject var model: AppModel
    @State private var errorMessage: String?

    var body: some View {
        Card {
            CardHeader(title: "Health access needed", symbol: "heart.circle")
            Text("You enabled data that HealthBridge has not asked for yet.")
                .font(.subheadline)
                .foregroundStyle(.secondary)
            Button("Review Health access") {
                Task {
                    do {
                        try await model.requestHealthAuthorization()
                    } catch {
                        errorMessage = error.localizedDescription
                    }
                }
            }
            .secondaryActionStyle()
            if let errorMessage {
                Text(errorMessage)
                    .font(.footnote)
                    .foregroundStyle(.red)
            }
        }
    }
}

// MARK: - Sync mode

private struct SyncModeCard: View {
    @EnvironmentObject var model: AppModel
    @EnvironmentObject var purchases: PurchaseManager
    @Binding var showPaywall: Bool

    var body: some View {
        Card {
            HStack {
                CardHeader(title: "Sync mode", symbol: "arrow.triangle.branch")
                if !purchases.isPro {
                    ProBadge()
                }
            }
            Picker("Sync mode", selection: modeBinding) {
                ForEach(SyncMode.allCases) { mode in
                    Text(mode.title).tag(mode)
                }
            }
            .pickerStyle(.segmented)

            if model.effectiveMode == .scheduled {
                Picker("Interval", selection: intervalBinding) {
                    ForEach(SyncInterval.allCases) { interval in
                        Text(interval.title).tag(interval)
                    }
                }
                .pickerStyle(.menu)
            }

            Text(model.effectiveMode.explanation)
                .font(.footnote)
                .foregroundStyle(.secondary)
                .fixedSize(horizontal: false, vertical: true)

            if !purchases.isPro && model.settings.mode != .manual {
                Text("Your plan currently allows manual sync only.")
                    .font(.footnote)
                    .foregroundStyle(.orange)
            }
        }
    }

    private var modeBinding: Binding<SyncMode> {
        Binding(
            get: { model.effectiveMode },
            set: { mode in
                if mode.requiresPro && !purchases.isPro {
                    showPaywall = true
                } else {
                    model.updateSettings { $0.mode = mode }
                }
            }
        )
    }

    private var intervalBinding: Binding<SyncInterval> {
        Binding(
            get: { model.settings.interval },
            set: { interval in model.updateSettings { $0.interval = interval } }
        )
    }
}

// MARK: - Navigation

private struct NavigationTiles: View {
    @EnvironmentObject var model: AppModel

    var body: some View {
        HStack(spacing: 12) {
            NavigationLink {
                MetricsView()
            } label: {
                tile(
                    title: "Metrics",
                    detail: "\(model.settings.categories.count) of \(MetricCategory.allCases.count) categories",
                    symbol: "square.grid.2x2.fill",
                    tint: .pink
                )
            }
            NavigationLink {
                ActivityLogView()
            } label: {
                tile(
                    title: "Activity",
                    detail: model.logEntries.isEmpty ? "No entries yet" : "\(model.logEntries.count) entries",
                    symbol: "list.bullet.rectangle.fill",
                    tint: .blue
                )
            }
        }
        .buttonStyle(.plain)
    }

    private func tile(title: String, detail: String, symbol: String, tint: Color) -> some View {
        Card(spacing: 8) {
            Image(systemName: symbol)
                .font(.title2)
                .foregroundStyle(tint)
            Text(title)
                .font(.headline)
            Text(detail)
                .font(.caption)
                .foregroundStyle(.secondary)
        }
    }
}

private struct ProTeaser: View {
    @Binding var showPaywall: Bool

    var body: some View {
        Button {
            showPaywall = true
        } label: {
            HStack(spacing: 12) {
                Image(systemName: "sparkles")
                    .font(.title2)
                    .foregroundStyle(.orange)
                VStack(alignment: .leading, spacing: 2) {
                    Text("HealthBridge Pro")
                        .font(.headline)
                    Text("Automatic background sync, \(HistoryWindow.proLimit.title) of history, workout heart-rate details.")
                        .font(.caption)
                        .foregroundStyle(.secondary)
                        .multilineTextAlignment(.leading)
                }
                Spacer()
                Image(systemName: "chevron.right")
                    .foregroundStyle(.tertiary)
            }
            .padding(16)
            .background(
                RoundedRectangle(cornerRadius: 20, style: .continuous)
                    .fill(Color.orange.opacity(0.1))
            )
        }
        .buttonStyle(.plain)
    }
}
