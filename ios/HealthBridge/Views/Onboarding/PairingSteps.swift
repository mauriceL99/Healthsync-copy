import SwiftUI
import UIKit

/// Scan the QR code shown by the agent CLI, or paste the `HB1:` code.
struct PairStep: View {
    var onDecoded: (PairingPayload) -> Void
    var onBack: () -> Void

    @State private var scanner: ScannerAvailability = .checking
    @State private var pastedCode = ""
    @State private var errorMessage: String?
    @FocusState var codeFieldFocused: Bool

    var body: some View {
        StepScaffold(
            symbol: "qrcode.viewfinder",
            title: "Pair with your agent",
            subtitle: "On your computer, run the HealthBridge pairing command. Scan the QR code it shows, or paste the pairing code."
        ) {
            scannerSection
            pasteSection
            if let errorMessage {
                Label(errorMessage, systemImage: "exclamationmark.triangle.fill")
                    .font(.subheadline)
                    .foregroundStyle(.red)
                    .frame(maxWidth: .infinity, alignment: .leading)
            }
        } footer: {
            Button {
                decode(pastedCode)
            } label: {
                Text("Continue")
                    .frame(maxWidth: .infinity)
            }
            .primaryActionStyle()
            .disabled(pastedCode.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)

            Button("Back", action: onBack)
                .font(.subheadline)
        }
        .task {
            scanner = await ScannerAvailability.resolve()
        }
    }

    @ViewBuilder
    private var scannerSection: some View {
        ZStack {
            RoundedRectangle(cornerRadius: 20, style: .continuous)
                .fill(Color(.secondarySystemBackground))
            switch scanner {
            case .checking:
                ProgressView()
            case .ready:
                QRScannerView { code in
                    decode(code)
                } onStartFailed: {
                    scanner = .failed
                }
                .clipShape(RoundedRectangle(cornerRadius: 20, style: .continuous))
            case .denied:
                VStack(spacing: 10) {
                    Image(systemName: "camera.fill")
                        .font(.largeTitle)
                        .foregroundStyle(.secondary)
                    Text("Camera access is off. Allow it in Settings, or paste the pairing code below.")
                        .font(.subheadline)
                        .multilineTextAlignment(.center)
                        .foregroundStyle(.secondary)
                    Button("Open Settings") {
                        if let url = URL(string: UIApplication.openSettingsURLString) {
                            UIApplication.shared.open(url)
                        }
                    }
                    .buttonStyle(.bordered)
                }
                .padding()
            case .unsupported, .failed:
                VStack(spacing: 10) {
                    Image(systemName: "qrcode")
                        .font(.largeTitle)
                        .foregroundStyle(.secondary)
                    Text(scanner == .failed
                        ? "The camera could not start. Paste the pairing code below."
                        : "Scanning is not supported on this device. Paste the pairing code below.")
                        .font(.subheadline)
                        .multilineTextAlignment(.center)
                        .foregroundStyle(.secondary)
                }
                .padding()
            }
        }
        .frame(height: 280)
    }

    private var pasteSection: some View {
        VStack(alignment: .leading, spacing: 8) {
            HStack {
                Text("Pairing code")
                    .font(.headline)
                Spacer()
                PasteButton(payloadType: String.self) { strings in
                    guard let first = strings.first else { return }
                    Task { @MainActor in
                        pastedCode = first.trimmingCharacters(in: .whitespacesAndNewlines)
                        decode(pastedCode)
                    }
                }
                .labelStyle(.titleAndIcon)
                .buttonBorderShape(.capsule)
                .controlSize(.small)
            }
            TextField("HB1:…", text: $pastedCode, axis: .vertical)
                .lineLimit(2...5)
                .font(.system(.footnote, design: .monospaced))
                .textInputAutocapitalization(.never)
                .autocorrectionDisabled()
                .focused($codeFieldFocused)
                .padding(12)
                .tileBackground(cornerRadius: 12)
                .submitLabel(.done)
        }
    }

    private func decode(_ text: String) {
        do {
            let payload = try PairingCode.decode(text)
            errorMessage = nil
            codeFieldFocused = false
            onDecoded(payload)
        } catch {
            errorMessage = error.localizedDescription
        }
    }
}

/// Shows the safety code so the user can compare it with the CLI before linking.
struct ConfirmPairingStep: View {
    let payload: PairingPayload
    var onLinked: () -> Void
    var onCancel: () -> Void

    @EnvironmentObject var model: AppModel
    @State private var isLinking = false
    @State private var errorMessage: String?

    var body: some View {
        StepScaffold(
            symbol: "checkmark.shield.fill",
            title: "Compare the safety code",
            subtitle: "Your agent shows the same code in the terminal. Only continue if both codes match exactly."
        ) {
            SafetyCodeView(code: payload.safetyCode)

            Card {
                InfoRow(label: "Agent", value: payload.displayName, symbol: "terminal")
                Divider()
                InfoRow(label: "Relay", value: payload.relayHost, symbol: "server.rack")
                if payload.relay.lowercased().hasPrefix("http://") {
                    Label("Unencrypted development relay (local network). Your health data is still end-to-end encrypted.", systemImage: "exclamationmark.triangle")
                        .font(.footnote)
                        .foregroundStyle(.orange)
                }
            }

            if let errorMessage {
                Label(errorMessage, systemImage: "exclamationmark.triangle.fill")
                    .font(.subheadline)
                    .foregroundStyle(.red)
                    .frame(maxWidth: .infinity, alignment: .leading)
            }
        } footer: {
            Button {
                Task { await link() }
            } label: {
                HStack {
                    if isLinking {
                        ProgressView()
                            .tint(.white)
                    }
                    Text(isLinking ? "Linking…" : "Codes match — link this iPhone")
                }
                .frame(maxWidth: .infinity)
            }
            .primaryActionStyle()
            .disabled(isLinking)

            Button("Codes don't match", role: .destructive, action: onCancel)
                .font(.subheadline)
                .disabled(isLinking)
        }
    }

    @MainActor
    private func link() async {
        isLinking = true
        errorMessage = nil
        defer { isLinking = false }
        do {
            try await model.link(payload)
            UINotificationFeedbackGenerator().notificationOccurred(.success)
            onLinked()
        } catch {
            errorMessage = error.localizedDescription
        }
    }
}
