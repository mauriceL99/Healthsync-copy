import CryptoKit
import Foundation

/// Constants and small derivations of protocol HB1 (docs/PROTOCOL.md).
enum HB1 {
    static let version = 1
    static let pairingPrefix = "HB1:"
    static let envelopeAlgorithm = "HB1-X25519-HKDF-SHA256-CHACHA20POLY1305"
    static let envelopeKeyInfo = "HB1-ENVELOPE-KEY"
    static let envelopeAADPrefix = "HB1-ENVELOPE"
    static let requestSignaturePrefix = "HB1-REQUEST"
    static let safetyPrefix = "HB1-SAFETY"

    /// The relay accepts at most 32 envelopes per `POST /v1/envelopes`.
    static let maxEnvelopesPerRequest = 32
    /// The relay accepts a request body of at most 25 MiB; stay well below it.
    static let maxUploadBodyBytes = 16 * 1024 * 1024

    /// `kid = b64u(SHA256(enc_pub)[0..12])`
    static func keyID(encryptionPublicKey: Data) -> String {
        let digest = Array(SHA256.hash(data: encryptionPublicKey))
        return B64URL.encode(Data(digest[0..<12]))
    }

    /// `pairing_hash = b64(SHA256(secret))`
    static func pairingHash(secret: Data) -> String {
        B64.encode(Data(SHA256.hash(data: secret)))
    }

    /// Safety code `XXXX-XXXX-XXXX`:
    /// `upper(hex(SHA256("HB1-SAFETY" ‖ 0x00 ‖ account ‖ 0x00 ‖ sig_pub ‖ enc_pub)[0..6]))`.
    static func safetyCode(account: String, signingPublicKey: Data, encryptionPublicKey: Data) -> String {
        var input = Data(safetyPrefix.utf8)
        input.append(UInt8(0))
        input.append(Data(account.utf8))
        input.append(UInt8(0))
        input.append(signingPublicKey)
        input.append(encryptionPublicKey)
        let digest = Array(SHA256.hash(data: input))
        let hex = Data(digest[0..<6]).hexString.uppercased()
        let characters = Array(hex)
        return String(characters[0..<4]) + "-" + String(characters[4..<8]) + "-" + String(characters[8..<12])
    }

    /// `"hb_" + b64u(16 bytes)` → `^hb_[A-Za-z0-9_-]{22}$`
    static func isValidAccountID(_ account: String) -> Bool {
        isValidPrefixedID(account, prefix: "hb_")
    }

    /// `"dev_" + b64u(16 bytes)` → `^dev_[A-Za-z0-9_-]{22}$`
    static func isValidDeviceID(_ deviceID: String) -> Bool {
        isValidPrefixedID(deviceID, prefix: "dev_")
    }

    private static func isValidPrefixedID(_ value: String, prefix: String) -> Bool {
        guard value.hasPrefix(prefix) else { return false }
        let rest = value.utf8.dropFirst(prefix.utf8.count)
        guard rest.count == 22 else { return false }
        return rest.allSatisfy { byte in
            switch byte {
            case UInt8(ascii: "A")...UInt8(ascii: "Z"),
                 UInt8(ascii: "a")...UInt8(ascii: "z"),
                 UInt8(ascii: "0")...UInt8(ascii: "9"),
                 UInt8(ascii: "-"),
                 UInt8(ascii: "_"):
                return true
            default:
                return false
            }
        }
    }

    /// Removes control characters and trims a human label (agent or device name).
    static func cleanLabel(_ value: String, maxLength: Int = 64) -> String {
        let scalars = value.trimmingCharacters(in: .whitespacesAndNewlines).unicodeScalars.filter { scalar in
            !(scalar.value < 0x20 || scalar.value == 0x7f)
        }
        var cleaned = String(String.UnicodeScalarView(scalars))
        if cleaned.unicodeScalars.count > maxLength {
            cleaned = String(String.UnicodeScalarView(cleaned.unicodeScalars.prefix(maxLength)))
        }
        return cleaned
    }
}
