import AVFoundation
import os
import SwiftUI
import UIKit
import Vision
import VisionKit

/// Live QR scanner based on VisionKit's `DataScannerViewController`.
/// Reports the first payload that starts with `HB1:`, or once if the camera cannot start.
struct QRScannerView: UIViewControllerRepresentable {
    var onCode: (String) -> Void
    var onStartFailed: () -> Void

    func makeCoordinator() -> Coordinator {
        Coordinator(onCode: onCode)
    }

    func makeUIViewController(context: Context) -> DataScannerViewController {
        let scanner = DataScannerViewController(
            recognizedDataTypes: [.barcode(symbologies: [.qr])],
            qualityLevel: .balanced,
            recognizesMultipleItems: false,
            isHighFrameRateTrackingEnabled: false,
            isPinchToZoomEnabled: true,
            isGuidanceEnabled: true,
            isHighlightingEnabled: true
        )
        scanner.delegate = context.coordinator
        return scanner
    }

    func updateUIViewController(_ scanner: DataScannerViewController, context: Context) {
        let coordinator = context.coordinator
        coordinator.onCode = onCode
        // Try once: retrying on every view update would loop once the failure changes state.
        guard !scanner.isScanning, !coordinator.startFailed else { return }
        do {
            try scanner.startScanning()
        } catch {
            coordinator.startFailed = true
            Log.ui.error("Starting the QR scanner failed: \(error.localizedDescription, privacy: .public)")
            // Report after this view update, not during it.
            let onStartFailed = onStartFailed
            Task { @MainActor in onStartFailed() }
        }
    }

    static func dismantleUIViewController(_ scanner: DataScannerViewController, coordinator: Coordinator) {
        scanner.stopScanning()
    }

    @MainActor
    final class Coordinator: NSObject, DataScannerViewControllerDelegate {
        var onCode: (String) -> Void
        var startFailed = false
        private var lastDelivered: String?

        init(onCode: @escaping (String) -> Void) {
            self.onCode = onCode
        }

        func dataScanner(
            _ dataScanner: DataScannerViewController,
            didAdd addedItems: [RecognizedItem],
            allItems: [RecognizedItem]
        ) {
            handle(addedItems)
        }

        func dataScanner(_ dataScanner: DataScannerViewController, didTapOn item: RecognizedItem) {
            handle([item])
        }

        private func handle(_ items: [RecognizedItem]) {
            for item in items {
                guard case .barcode(let barcode) = item,
                      let payload = barcode.payloadStringValue?.trimmingCharacters(in: .whitespacesAndNewlines),
                      payload.hasPrefix(HB1.pairingPrefix),
                      payload != lastDelivered else { continue }
                lastDelivered = payload
                UINotificationFeedbackGenerator().notificationOccurred(.success)
                onCode(payload)
                return
            }
        }
    }
}

/// Camera availability for the scanner.
enum ScannerAvailability: Equatable {
    case checking
    case ready
    case denied
    case unsupported
    /// Supported and allowed, but the camera did not start (e.g. in use by another app).
    case failed

    @MainActor
    static func resolve() async -> ScannerAvailability {
        guard DataScannerViewController.isSupported else { return .unsupported }
        switch AVCaptureDevice.authorizationStatus(for: .video) {
        case .authorized:
            return DataScannerViewController.isAvailable ? .ready : .denied
        case .notDetermined:
            let granted = await AVCaptureDevice.requestAccess(for: .video)
            return granted && DataScannerViewController.isAvailable ? .ready : .denied
        case .denied, .restricted:
            return .denied
        @unknown default:
            return .denied
        }
    }
}
