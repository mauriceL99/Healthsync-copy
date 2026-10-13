import SwiftUI

/// Category toggles plus the full list of metrics each category sends.
struct MetricsView: View {
    @EnvironmentObject var model: AppModel
    @EnvironmentObject var purchases: PurchaseManager
    @State private var showPaywall = false
    @State private var authorizationError: String?

    var body: some View {
        List {
            Section {
                CategoryToggleList(showPaywall: $showPaywall)
                    .listRowInsets(EdgeInsets(top: 12, leading: 12, bottom: 12, trailing: 12))
                    .listRowBackground(Color.clear)
            } header: {
                Text("Shared categories")
            } footer: {
                Text("Disabled categories are never read from Apple Health. Your agent keeps data it already received.")
            }

            if model.needsHealthAuthorization {
                Section {
                    Button("Review Health access") {
                        Task {
                            do {
                                try await model.requestHealthAuthorization()
                            } catch {
                                authorizationError = error.localizedDescription
                            }
                        }
                    }
                    if let authorizationError {
                        Text(authorizationError)
                            .font(.footnote)
                            .foregroundStyle(.red)
                    }
                } footer: {
                    Text("Newly enabled data needs your permission before it can be synced.")
                }
            }

            ForEach(MetricCategory.allCases) { category in
                Section {
                    if category == .workouts {
                        ForEach(MetricCatalog.workoutFields) { field in
                            Label(field.name, systemImage: field.symbol)
                        }
                        ForEach(MetricCatalog.workoutHeartRateDetailFields) { field in
                            HStack {
                                Label(field.name, systemImage: field.symbol)
                                Spacer()
                                if !purchases.isPro {
                                    ProBadge()
                                } else if !model.heartRateDetailsActive {
                                    Text("Off")
                                        .font(.caption)
                                        .foregroundStyle(.secondary)
                                }
                            }
                        }
                    } else {
                        ForEach(MetricCatalog.metrics(in: category)) { metric in
                            MetricRow(metric: metric)
                        }
                    }
                } header: {
                    HStack(spacing: 6) {
                        Image(systemName: category.symbol)
                            .foregroundStyle(category.tint)
                        Text(category.title)
                        Spacer()
                        Text(model.settings.categories.contains(category) ? "On" : "Off")
                            .foregroundStyle(model.settings.categories.contains(category) ? Color.green : Color.secondary)
                    }
                }
                .opacity(model.settings.categories.contains(category) ? 1 : 0.5)
            }
        }
        .navigationTitle("Metrics")
        .navigationBarTitleDisplayMode(.inline)
        .paywallSheet(isPresented: $showPaywall)
    }
}

private struct MetricRow: View {
    let metric: MetricDefinition

    var body: some View {
        HStack(spacing: 12) {
            Image(systemName: metric.symbol)
                .foregroundStyle(metric.category.tint)
                .frame(width: 24)
            VStack(alignment: .leading, spacing: 2) {
                Text(metric.displayName)
                Text(detail)
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }
        }
    }

    private var detail: String {
        let unit = metric.unit.symbol
        let aggregation = metric.category == .sleep ? "per night" : metric.aggregation.title
        return unit.isEmpty ? aggregation : "\(unit) · \(aggregation)"
    }
}

/// Sync history: timestamps, envelope counts, sizes and errors. Never health values.
struct ActivityLogView: View {
    @EnvironmentObject var model: AppModel
    @State private var confirmClear = false

    var body: some View {
        Group {
            if model.logEntries.isEmpty {
                ContentUnavailableView(
                    "No activity yet",
                    systemImage: "list.bullet.rectangle",
                    description: Text("Syncs, pairing and errors appear here. Health values are never logged.")
                )
            } else {
                List {
                    Section {
                        ForEach(model.logEntries) { entry in
                            ActivityRow(entry: entry)
                        }
                    } footer: {
                        Text("Only metadata is logged: time, envelope count, encrypted size and errors. Never health values.")
                    }
                }
            }
        }
        .navigationTitle("Activity")
        .navigationBarTitleDisplayMode(.inline)
        .toolbar {
            ToolbarItem(placement: .topBarTrailing) {
                Button("Clear", role: .destructive) {
                    confirmClear = true
                }
                .disabled(model.logEntries.isEmpty)
            }
        }
        .confirmationDialog("Clear the activity log?", isPresented: $confirmClear, titleVisibility: .visible) {
            Button("Clear log", role: .destructive) {
                ActivityLogStore.shared.clear()
            }
        }
    }
}

private struct ActivityRow: View {
    let entry: ActivityLogEntry

    var body: some View {
        HStack(alignment: .top, spacing: 12) {
            Image(systemName: symbol)
                .font(.title3)
                .foregroundStyle(tint)
                .frame(width: 28)
            VStack(alignment: .leading, spacing: 4) {
                HStack {
                    Text(title)
                        .font(.subheadline.weight(.semibold))
                    Spacer()
                    Text(Formatting.dateTime(entry.date))
                        .font(.caption)
                        .foregroundStyle(.secondary)
                }
                if let detail {
                    Text(detail)
                        .font(.caption)
                        .foregroundStyle(.secondary)
                }
                if let error = entry.errorMessage {
                    Text(error)
                        .font(.caption)
                        .foregroundStyle(entry.event == .sync ? Color.red : Color.secondary)
                }
                if let warning = entry.warning {
                    Text(warning)
                        .font(.caption)
                        .foregroundStyle(.orange)
                }
            }
        }
        .padding(.vertical, 2)
    }

    private var title: String {
        switch entry.event {
        case .sync:
            let kind = entry.kind == .backfill ? "History import" : "Sync"
            let trigger = entry.trigger.map { " · \($0.title)" } ?? ""
            return kind + trigger
        case .paired:
            return "Paired with agent"
        case .unpaired:
            return "Unpaired"
        case .unlinkedByAgent:
            return "Unlinked by agent"
        }
    }

    private var detail: String? {
        guard entry.event == .sync, entry.succeeded else { return nil }
        var parts = [
            entry.envelopeCount == 1 ? "1 envelope" : "\(entry.envelopeCount) envelopes",
            Formatting.bytes(entry.ciphertextBytes),
            entry.dayCount == 1 ? "1 day" : "\(entry.dayCount) days",
        ]
        if entry.duration > 0 {
            parts.append(String(format: "%.1f s", entry.duration))
        }
        return parts.joined(separator: " · ")
    }

    private var symbol: String {
        switch entry.event {
        case .sync: return entry.succeeded ? "checkmark.circle.fill" : "xmark.octagon.fill"
        case .paired: return "link.circle.fill"
        case .unpaired, .unlinkedByAgent: return "link.badge.plus"
        }
    }

    private var tint: Color {
        switch entry.event {
        case .sync: return entry.succeeded ? .green : .red
        case .paired: return .accentColor
        case .unpaired, .unlinkedByAgent: return .orange
        }
    }
}
