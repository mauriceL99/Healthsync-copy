import SwiftUI

// MARK: - Buttons

extension View {
    /// Prominent button for the main action of a screen. It sizes to its label; give the
    /// label `.frame(maxWidth: .infinity)` for a full-width button.
    func primaryActionStyle() -> some View {
        buttonStyle(.borderedProminent)
            .controlSize(.large)
            .buttonBorderShape(.roundedRectangle(radius: 14))
    }

    /// Secondary button; sizes to its label like `primaryActionStyle()`.
    func secondaryActionStyle() -> some View {
        buttonStyle(.bordered)
            .controlSize(.large)
            .buttonBorderShape(.roundedRectangle(radius: 14))
    }
}

// MARK: - Cards

struct Card<Content: View>: View {
    @ViewBuilder var content: Content

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            content
        }
        .frame(maxWidth: .infinity, alignment: .leading)
        .padding(16)
        .background(
            RoundedRectangle(cornerRadius: 20, style: .continuous)
                .fill(Color(.secondarySystemGroupedBackground))
        )
    }
}

struct CardHeader: View {
    let title: String
    let symbol: String

    var body: some View {
        HStack(spacing: 8) {
            Image(systemName: symbol)
                .foregroundStyle(.tint)
            Text(title)
                .font(.headline)
            Spacer()
        }
    }
}

struct InfoRow: View {
    let label: String
    let value: String
    var symbol: String?
    var monospaced = false

    var body: some View {
        HStack(alignment: .firstTextBaseline, spacing: 10) {
            if let symbol {
                Image(systemName: symbol)
                    .frame(width: 20)
                    .foregroundStyle(.secondary)
            }
            Text(label)
                .foregroundStyle(.secondary)
            Spacer(minLength: 12)
            Text(value)
                .fontDesign(monospaced ? .monospaced : .default)
                .multilineTextAlignment(.trailing)
                .lineLimit(2)
                .truncationMode(.middle)
        }
        .font(.subheadline)
    }
}

struct ProBadge: View {
    var body: some View {
        Text("PRO")
            .font(.caption2.weight(.heavy))
            .padding(.horizontal, 6)
            .padding(.vertical, 2)
            .background(Capsule().fill(Color.orange.opacity(0.2)))
            .foregroundStyle(.orange)
            .accessibilityLabel("Requires HealthBridge Pro")
    }
}

struct FeatureRow: View {
    let symbol: String
    let title: String
    let text: String

    var body: some View {
        HStack(alignment: .top, spacing: 14) {
            Image(systemName: symbol)
                .font(.title2)
                .symbolRenderingMode(.hierarchical)
                .foregroundStyle(.tint)
                .frame(width: 36)
            VStack(alignment: .leading, spacing: 3) {
                Text(title)
                    .font(.headline)
                Text(text)
                    .font(.subheadline)
                    .foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true)
            }
            Spacer(minLength: 0)
        }
    }
}

struct DisclaimerCard: View {
    var body: some View {
        HStack(alignment: .top, spacing: 12) {
            Image(systemName: "stethoscope")
                .foregroundStyle(.orange)
            Text("HealthBridge is not a medical device. It does not diagnose, treat, cure or prevent any disease, and summaries produced by AI agents can be wrong. Ask a doctor about medical questions.")
                .font(.footnote)
                .foregroundStyle(.secondary)
                .fixedSize(horizontal: false, vertical: true)
        }
        .padding(14)
        .background(
            RoundedRectangle(cornerRadius: 14, style: .continuous)
                .fill(Color.orange.opacity(0.08))
        )
    }
}

struct SafetyCodeView: View {
    let code: String

    var body: some View {
        Text(code)
            .font(.system(.title, design: .monospaced).weight(.bold))
            .kerning(2)
            .padding(.vertical, 14)
            .padding(.horizontal, 20)
            .frame(maxWidth: .infinity)
            .background(
                RoundedRectangle(cornerRadius: 16, style: .continuous)
                    .strokeBorder(Color.accentColor, lineWidth: 2)
            )
            .accessibilityLabel("Safety code \(code.map { String($0) }.joined(separator: " "))")
            .textSelection(.enabled)
    }
}

/// Layout shared by all onboarding steps: scrollable content plus a pinned footer.
struct StepScaffold<Content: View, Footer: View>: View {
    let symbol: String
    let title: String
    let subtitle: String
    @ViewBuilder var content: Content
    @ViewBuilder var footer: Footer

    var body: some View {
        VStack(spacing: 0) {
            ScrollView {
                VStack(spacing: 20) {
                    Image(systemName: symbol)
                        .font(.system(size: 54, weight: .semibold))
                        .symbolRenderingMode(.hierarchical)
                        .foregroundStyle(.tint)
                        .padding(.top, 24)
                    Text(title)
                        .font(.largeTitle.bold())
                        .multilineTextAlignment(.center)
                    Text(subtitle)
                        .font(.body)
                        .foregroundStyle(.secondary)
                        .multilineTextAlignment(.center)
                        .fixedSize(horizontal: false, vertical: true)
                    content
                }
                .padding(.horizontal, 24)
                .padding(.bottom, 24)
            }
            VStack(spacing: 10) {
                footer
            }
            .padding(.horizontal, 24)
            .padding(.top, 12)
            .padding(.bottom, 8)
            .background(.bar)
        }
    }
}

// MARK: - Formatting

enum Formatting {
    static func bytes(_ count: Int) -> String {
        Int64(count).formatted(.byteCount(style: .file))
    }

    static func relative(_ date: Date) -> String {
        date.formatted(.relative(presentation: .named))
    }

    static func dateTime(_ date: Date) -> String {
        date.formatted(date: .abbreviated, time: .shortened)
    }
}

extension MetricCategory {
    var tint: Color {
        switch self {
        case .activity: return .orange
        case .heart: return .red
        case .sleep: return .indigo
        case .body: return .purple
        case .vitals: return .pink
        case .nutrition: return .green
        case .mindfulness: return .teal
        case .workouts: return .blue
        }
    }
}
