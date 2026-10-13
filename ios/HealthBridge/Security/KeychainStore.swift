import CryptoKit
import Foundation
import os
import Security

enum KeychainError: LocalizedError {
    case unexpectedStatus(OSStatus)
    case corruptItem

    var errorDescription: String? {
        switch self {
        case .unexpectedStatus(let status):
            if status == errSecInteractionNotAllowed {
                return "The keychain is locked. Unlock your iPhone and try again."
            }
            return "Keychain error (\(status))."
        case .corruptItem:
            return "A keychain item is damaged. Pair again."
        }
    }
}

/// Thin wrapper around generic-password keychain items.
///
/// Items use `kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly`, so background sync works
/// while the phone is locked (after the first unlock since boot) and nothing is restored to
/// another device from a backup.
enum KeychainStore {
    enum Item: String, CaseIterable {
        case deviceSigningKey = "device-signing-key"
        case pairing = "pairing-record"
    }

    private static let service = "com.example.healthbridge"

    private static func baseQuery(for item: Item) -> [String: Any] {
        [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: service,
            kSecAttrAccount as String: item.rawValue,
        ]
    }

    static func set(_ data: Data, for item: Item) throws {
        let query = baseQuery(for: item)
        let attributes: [String: Any] = [
            kSecValueData as String: data,
            kSecAttrAccessible as String: kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly,
        ]
        let updateStatus = SecItemUpdate(query as CFDictionary, attributes as CFDictionary)
        switch updateStatus {
        case errSecSuccess:
            return
        case errSecItemNotFound:
            var addQuery = query
            addQuery.merge(attributes) { _, new in new }
            let addStatus = SecItemAdd(addQuery as CFDictionary, nil)
            guard addStatus == errSecSuccess else {
                throw KeychainError.unexpectedStatus(addStatus)
            }
        default:
            throw KeychainError.unexpectedStatus(updateStatus)
        }
    }

    static func data(for item: Item) throws -> Data? {
        var query = baseQuery(for: item)
        query[kSecReturnData as String] = true
        query[kSecMatchLimit as String] = kSecMatchLimitOne
        var result: CFTypeRef?
        let status = SecItemCopyMatching(query as CFDictionary, &result)
        switch status {
        case errSecSuccess:
            guard let data = result as? Data else {
                throw KeychainError.corruptItem
            }
            return data
        case errSecItemNotFound:
            return nil
        default:
            throw KeychainError.unexpectedStatus(status)
        }
    }

    /// Deleting an item that does not exist is not an error.
    static func delete(_ item: Item) throws {
        let status = SecItemDelete(baseQuery(for: item) as CFDictionary)
        guard status == errSecSuccess || status == errSecItemNotFound else {
            throw KeychainError.unexpectedStatus(status)
        }
    }

    /// Tries every item, then throws the first failure.
    static func deleteAll() throws {
        var firstError: Error?
        for item in Item.allCases {
            do {
                try delete(item)
            } catch {
                firstError = firstError ?? error
            }
        }
        if let firstError {
            throw firstError
        }
    }
}

/// The device's Ed25519 identity (`device_sig`).
enum DeviceIdentityStore {
    static func loadSigningKey() throws -> Curve25519.Signing.PrivateKey? {
        guard let raw = try KeychainStore.data(for: .deviceSigningKey) else {
            return nil
        }
        do {
            return try Curve25519.Signing.PrivateKey(rawRepresentation: raw)
        } catch {
            throw KeychainError.corruptItem
        }
    }

    /// Returns the existing key or creates and stores a new one.
    static func loadOrCreateSigningKey() throws -> Curve25519.Signing.PrivateKey {
        if let existing = try loadSigningKey() {
            return existing
        }
        let key = Curve25519.Signing.PrivateKey()
        try KeychainStore.set(key.rawRepresentation, for: .deviceSigningKey)
        return key
    }
}

/// Everything the app keeps about the paired agent. Contains only public keys and IDs;
/// the pairing secret is not stored after the device has been linked.
struct PairingRecord: Codable, Equatable, Sendable {
    var relay: String
    var account: String
    /// Agent Ed25519 public key, b64.
    var agentSigningKey: String
    /// Agent X25519 public key, b64.
    var agentEncryptionKey: String
    var agentName: String?
    var deviceID: String
    var linkedAt: Date
    var safetyCode: String
    var keyID: String

    var displayName: String {
        if let agentName, !agentName.isEmpty {
            return agentName
        }
        return "AI Agent"
    }

    var relayHost: String {
        guard let components = URLComponents(string: relay), let host = components.host else {
            return relay
        }
        if let port = components.port {
            return "\(host):\(port)"
        }
        return host
    }

    func recipientKey() throws -> Curve25519.KeyAgreement.PublicKey {
        guard let raw = B64.decode(agentEncryptionKey, length: 32),
              let key = try? Curve25519.KeyAgreement.PublicKey(rawRepresentation: raw) else {
            throw EnvelopeError.invalidRecipientKey
        }
        return key
    }
}

enum PairingStore {
    private static let encoder = JSONEncoder()
    private static let decoder = JSONDecoder()

    /// The stored record, or nil when this iPhone is not paired. Throws when the keychain
    /// cannot be read right now (for example before the first unlock after a reboot) or the
    /// record is damaged, so callers can tell "not paired" from "unknown".
    static func loadRecord() throws -> PairingRecord? {
        guard let data = try KeychainStore.data(for: .pairing) else {
            return nil
        }
        do {
            return try decoder.decode(PairingRecord.self, from: data)
        } catch {
            throw KeychainError.corruptItem
        }
    }

    /// For UI state in the foreground, where the keychain is readable: an unreadable or
    /// damaged record is logged and shown as "not paired", so the user can pair again
    /// (saving a new record replaces the damaged one).
    static func load() -> PairingRecord? {
        do {
            return try loadRecord()
        } catch {
            Log.storage.error("Pairing record unreadable: \(error.localizedDescription, privacy: .public)")
            return nil
        }
    }

    static func save(_ record: PairingRecord) throws {
        try KeychainStore.set(try encoder.encode(record), for: .pairing)
    }

    static func delete() throws {
        try KeychainStore.delete(.pairing)
    }

    /// True when a pairing record exists (a damaged one included: syncs then fail with a
    /// visible "pair again" error instead of silently doing nothing), false when there is
    /// none, nil when the keychain cannot be read right now, so callers can avoid acting
    /// on a guess.
    static func isPairedIfKnown() -> Bool? {
        do {
            return try KeychainStore.data(for: .pairing) != nil
        } catch KeychainError.corruptItem {
            return true
        } catch {
            return nil
        }
    }
}
