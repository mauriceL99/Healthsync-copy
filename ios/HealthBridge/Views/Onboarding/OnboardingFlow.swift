import SwiftUI

/// Welcome → Pair → Confirm safety code → Categories → Health access → History → First sync.
struct OnboardingFlow: View {
    enum Step: Int, Comparable, CaseIterable {
        case welcome
        case pair
        case confirm
        case categories
        case health
        case history
        case firstSync

        static func < (lhs: Step, rhs: Step) -> Bool {
            lhs.rawValue < rhs.rawValue
        }
    }

    @EnvironmentObject var model: AppModel
    @State private var step: Step = .welcome
    @State private var payload: PairingPayload?

    var body: some View {
        VStack(spacing: 0) {
            if step != .welcome {
                StepIndicator(current: step)
                    .padding(.top, 8)
            }
            Group {
                switch step {
                case .welcome:
                    WelcomeStep { go(.pair) }
                case .pair:
                    PairStep(
                        onDecoded: { decoded in
                            payload = decoded
                            go(.confirm)
                        },
                        onBack: { go(.welcome) }
                    )
                case .confirm:
                    if let payload {
                        ConfirmPairingStep(
                            payload: payload,
                            onLinked: { go(.categories) },
                            onCancel: {
                                self.payload = nil
                                go(.pair)
                            }
                        )
                    } else {
                        Color.clear.onAppear { go(.pair) }
                    }
                case .categories:
                    CategoriesStep { go(.health) }
                case .health:
                    HealthPermissionStep { go(.history) }
                case .history:
                    HistoryWindowStep { go(.firstSync) }
                case .firstSync:
                    FirstSyncStep { model.completeOnboarding() }
                }
            }
            .transition(.asymmetric(
                insertion: .move(edge: .trailing).combined(with: .opacity),
                removal: .move(edge: .leading).combined(with: .opacity)
            ))
        }
        .background(Color(.systemBackground))
        .onAppear {
            // Resume after pairing if the app was closed during setup.
            if model.isPaired && step < .categories {
                step = .categories
            }
        }
        .onChange(of: model.isPaired) { _, paired in
            if !paired && step >= .categories {
                payload = nil
                step = .welcome
            }
        }
    }

    private func go(_ next: Step) {
        withAnimation(.snappy) {
            step = next
        }
    }
}

private struct StepIndicator: View {
    let current: OnboardingFlow.Step

    private let steps: [OnboardingFlow.Step] = [.pair, .confirm, .categories, .health, .history, .firstSync]

    var body: some View {
        HStack(spacing: 6) {
            ForEach(steps, id: \.rawValue) { step in
                Capsule()
                    .fill(step <= current ? Color.accentColor : Color.secondary.opacity(0.25))
                    .frame(height: 4)
            }
        }
        .padding(.horizontal, 24)
        .accessibilityElement(children: .ignore)
        .accessibilityLabel("Step \((steps.firstIndex(of: current) ?? 0) + 1) of \(steps.count)")
    }
}

struct WelcomeStep: View {
    var onContinue: () -> Void

    var body: some View {
        StepScaffold(
            symbol: "heart.text.square.fill",
            title: "HealthBridge",
            subtitle: "Give your AI agent secure access to the Apple Health data you choose."
        ) {
            VStack(alignment: .leading, spacing: 18) {
                FeatureRow(
                    symbol: "lock.shield.fill",
                    title: "End-to-end encrypted",
                    text: "Your data is encrypted on this iPhone with your agent's key. The relay server only ever stores ciphertext it cannot read."
                )
                FeatureRow(
                    symbol: "terminal.fill",
                    title: "Made for AI agents",
                    text: "Works with Claude Code, Codex, OpenClaw and other agents through the HealthBridge command-line tool."
                )
                FeatureRow(
                    symbol: "slider.horizontal.3",
                    title: "You stay in control",
                    text: "Choose exactly which categories to share, sync manually or automatically, and unpair at any time."
                )
            }
            .padding(.top, 8)
            DisclaimerCard()
        } footer: {
            Button(action: onContinue) {
                Text("Get started")
                    .frame(maxWidth: .infinity)
            }
            .primaryActionStyle()
        }
    }
}
