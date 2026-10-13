import SwiftUI

/// Toggle list for metric categories, shared by onboarding and the metrics screen.
struct CategoryToggleList: View {
    @EnvironmentObject var model: AppModel
    @EnvironmentObject var purchases: PurchaseManager
    @Binding var showPaywall: Bool

    var body: some View {
        VStack(spacing: 10) {
            ForEach(MetricCategory.allCases) { category in
                Toggle(isOn: binding(for: category)) {
                    HStack(spacing: 12) {
                        Image(systemName: category.symbol)
                            .foregroundStyle(category.tint)
                            .frame(width: 28)
                        VStack(alignment: .leading, spacing: 2) {
                            Text(category.title)
                                .font(.headline)
                            Text(category.summary)
                                .font(.caption)
                                .foregroundStyle(.secondary)
                                .fixedSize(horizontal: false, vertical: true)
                        }
                    }
                }
                .padding(12)
                .background(
                    RoundedRectangle(cornerRadius: 14, style: .continuous)
                        .fill(Color(.secondarySystemBackground))
                )
            }
            if model.settings.categories.contains(.workouts) {
                Toggle(isOn: heartRateDetailsBinding) {
                    HStack(spacing: 12) {
                        Image(systemName: "heart.text.square")
                            .foregroundStyle(.red)
                            .frame(width: 28)
                        VStack(alignment: .leading, spacing: 2) {
                            HStack(spacing: 6) {
                                Text("Workout heart-rate details")
                                    .font(.headline)
                                if !purchases.isPro {
                                    ProBadge()
                                }
                            }
                            Text("Heart-rate zones, a curve with one point per 5 seconds and recovery after one minute.")
                                .font(.caption)
                                .foregroundStyle(.secondary)
                                .fixedSize(horizontal: false, vertical: true)
                        }
                    }
                }
                .padding(12)
                .background(
                    RoundedRectangle(cornerRadius: 14, style: .continuous)
                        .fill(Color(.secondarySystemBackground))
                )
            }
        }
    }

    private func binding(for category: MetricCategory) -> Binding<Bool> {
        Binding(
            get: { model.settings.categories.contains(category) },
            set: { enabled in
                model.updateSettings { settings in
                    if enabled {
                        _ = settings.categories.insert(category)
                    } else {
                        _ = settings.categories.remove(category)
                    }
                }
            }
        )
    }

    private var heartRateDetailsBinding: Binding<Bool> {
        Binding(
            get: { purchases.isPro && model.settings.workoutHeartRateDetails },
            set: { enabled in
                if enabled && !purchases.isPro {
                    showPaywall = true
                } else {
                    model.updateSettings { $0.workoutHeartRateDetails = enabled }
                }
            }
        )
    }
}

struct CategoriesStep: View {
    var onContinue: () -> Void

    @EnvironmentObject var model: AppModel
    @State private var showPaywall = false

    var body: some View {
        StepScaffold(
            symbol: "square.grid.2x2.fill",
            title: "Choose your data",
            subtitle: "Only the categories you enable are read from Apple Health and shared with your agent. You can change this later."
        ) {
            CategoryToggleList(showPaywall: $showPaywall)
        } footer: {
            Button(action: onContinue) {
                Text("Continue")
                    .frame(maxWidth: .infinity)
            }
            .primaryActionStyle()
            .disabled(model.settings.categories.isEmpty)
        }
        .paywallSheet(isPresented: $showPaywall)
    }
}

struct HealthPermissionStep: View {
    var onContinue: () -> Void

    @EnvironmentObject var model: AppModel
    @State private var isRequesting = false
    @State private var errorMessage: String?

    var body: some View {
        StepScaffold(
            symbol: "heart.circle.fill",
            title: "Allow Health access",
            subtitle: "iOS will now ask which data HealthBridge may read. HealthBridge never writes to Apple Health."
        ) {
            VStack(alignment: .leading, spacing: 16) {
                FeatureRow(
                    symbol: "checklist",
                    title: "Exactly your selection",
                    text: "HealthBridge only asks for the types in the categories you enabled."
                )
                FeatureRow(
                    symbol: "eye.slash",
                    title: "Private by design",
                    text: "iOS does not tell apps whether you allowed reading a type. Data you don't allow simply won't be sent."
                )
                FeatureRow(
                    symbol: "gearshape",
                    title: "Change it anytime",
                    text: "Settings › Health › Data Access & Devices › HealthBridge."
                )
            }
            if let errorMessage {
                Label(errorMessage, systemImage: "exclamationmark.triangle.fill")
                    .font(.subheadline)
                    .foregroundStyle(.red)
            }
        } footer: {
            Button {
                Task { await request() }
            } label: {
                HStack {
                    if isRequesting {
                        ProgressView()
                            .tint(.white)
                    }
                    Text("Continue")
                }
                .frame(maxWidth: .infinity)
            }
            .primaryActionStyle()
            .disabled(isRequesting)
        }
    }

    @MainActor
    private func request() async {
        isRequesting = true
        defer { isRequesting = false }
        do {
            try await model.requestHealthAuthorization()
            onContinue()
        } catch {
            errorMessage = (error as? LocalizedError)?.errorDescription ?? error.localizedDescription
        }
    }
}

struct HistoryWindowStep: View {
    var onContinue: () -> Void

    @EnvironmentObject var model: AppModel
    @EnvironmentObject var purchases: PurchaseManager
    @State private var showPaywall = false

    var body: some View {
        StepScaffold(
            symbol: "clock.arrow.circlepath",
            title: "How much history?",
            subtitle: "The first sync sends this many past days. After that, only recent days are sent."
        ) {
            HistoryWindowPicker(showPaywall: $showPaywall)
        } footer: {
            Button(action: onContinue) {
                Text("Continue")
                    .frame(maxWidth: .infinity)
            }
            .primaryActionStyle()
        }
        .paywallSheet(isPresented: $showPaywall)
    }
}

struct HistoryWindowPicker: View {
    @EnvironmentObject var model: AppModel
    @EnvironmentObject var purchases: PurchaseManager
    @Binding var showPaywall: Bool

    var body: some View {
        VStack(spacing: 10) {
            ForEach(HistoryWindow.allCases) { window in
                let locked = window.requiresPro && !purchases.isPro
                let selected = model.effectiveHistoryDays == window.rawValue
                Button {
                    if locked {
                        showPaywall = true
                    } else {
                        model.updateSettings { $0.historyWindow = window }
                    }
                } label: {
                    HStack {
                        Text(window.title)
                            .font(.headline)
                            .foregroundStyle(.primary)
                        if locked {
                            ProBadge()
                        }
                        Spacer()
                        Image(systemName: selected ? "checkmark.circle.fill" : "circle")
                            .foregroundStyle(selected ? Color.accentColor : Color.secondary)
                            .font(.title3)
                    }
                    .padding(14)
                    .background(
                        RoundedRectangle(cornerRadius: 14, style: .continuous)
                            .fill(Color(.secondarySystemBackground))
                    )
                }
                .buttonStyle(.plain)
            }
        }
    }
}

struct FirstSyncStep: View {
    var onFinish: () -> Void

    @EnvironmentObject var model: AppModel
    @State private var started = false

    var body: some View {
        StepScaffold(
            symbol: symbol,
            title: title,
            subtitle: subtitle
        ) {
            switch model.status {
            case .running(let progress):
                VStack(spacing: 12) {
                    ProgressView(value: progress.fraction)
                        .progressViewStyle(.linear)
                    Text(progress.message)
                        .font(.subheadline)
                        .foregroundStyle(.secondary)
                }
                .padding(.top, 12)
            case .succeeded(let report):
                Card {
                    InfoRow(label: "Days", value: "\(report.dayCount)", symbol: "calendar")
                    Divider()
                    InfoRow(label: "Encrypted envelopes", value: "\(report.envelopeCount)", symbol: "envelope.fill")
                    Divider()
                    InfoRow(label: "Uploaded", value: Formatting.bytes(report.ciphertextBytes), symbol: "arrow.up.circle")
                }
            case .failed(let message):
                Label(message, systemImage: "exclamationmark.triangle.fill")
                    .font(.subheadline)
                    .foregroundStyle(.red)
            case .idle:
                EmptyView()
            }
        } footer: {
            switch model.status {
            case .succeeded:
                Button(action: onFinish) {
                    Text("Done")
                        .frame(maxWidth: .infinity)
                }
                .primaryActionStyle()
            case .failed:
                Button {
                    Task { await model.syncNow(trigger: .onboarding) }
                } label: {
                    Text("Try again")
                        .frame(maxWidth: .infinity)
                }
                .primaryActionStyle()
                Button("Skip for now", action: onFinish)
                    .font(.subheadline)
            default:
                Button {} label: {
                    Text("Syncing…")
                        .frame(maxWidth: .infinity)
                }
                .primaryActionStyle()
                .disabled(true)
            }
        }
        .task {
            guard !started else { return }
            started = true
            await model.syncNow(trigger: .onboarding)
        }
    }

    private var symbol: String {
        switch model.status {
        case .succeeded: return "checkmark.seal.fill"
        case .failed: return "exclamationmark.icloud.fill"
        default: return "lock.icloud.fill"
        }
    }

    private var title: String {
        switch model.status {
        case .succeeded: return "You're all set"
        case .failed: return "Sync didn't finish"
        default: return "Encrypting & sending"
        }
    }

    private var subtitle: String {
        switch model.status {
        case .succeeded:
            return "Your agent can now fetch and decrypt the data. Try asking it how you slept this week."
        case .failed:
            return "You can try again now or later from the home screen."
        default:
            return "Your Health data is read and encrypted on this iPhone. Keep the app open until it's done."
        }
    }
}
