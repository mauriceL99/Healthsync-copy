import Foundation
import os

/// Unified-logging categories. Messages carry only error descriptions and counts,
/// never health values, keys or pairing secrets.
enum Log {
    private static let subsystem = Bundle.main.bundleIdentifier ?? "HealthBridge"

    static let storage = Logger(subsystem: subsystem, category: "storage")
    static let sync = Logger(subsystem: subsystem, category: "sync")
    static let background = Logger(subsystem: subsystem, category: "background")
    static let ui = Logger(subsystem: subsystem, category: "ui")
}
