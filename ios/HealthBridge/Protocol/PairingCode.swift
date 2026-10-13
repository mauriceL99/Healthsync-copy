import CryptoKit
import Darwin
import Foundation

/// A decoded and fully validated pairing code (`HB1:<b64u(JSON)>`, docs/PROTOCOL.md §3).
struct PairingPayload: Equatable, Identifiable, Sendable {
    var id: String { account }

    /// Relay base URL exactly as contained in the pairing code.
    let relay: String
    let account: String
    /// Agent Ed25519 public key (32 bytes).
    let agentSigningKey: Data
    /// Agent X25519 public key (32 bytes).
    let agentEncryptionKey: Data
    /// Pairing secret exactly as in the code (b64u, 32 bytes decoded).
    let secret: String
    /// Optional agent label, control characters removed.
    let name: String?

    let safetyCode: String
    let keyID: String

    var displayName: String {
        if let name, !name.isEmpty {
            return name
        }
        return "AI Agent"
    }

    var relayHost: String {
        URLComponents(string: relay)?.host ?? relay
    }
}

enum PairingCodeError: LocalizedError, Equatable {
    case missingPrefix
    case tooLong
    case invalidEncoding
    case invalidJSON
    case unsupportedVersion
    case invalidRelayURL
    case insecureRelayURL
    case invalidAccount
    case invalidSigningKey
    case invalidEncryptionKey
    case invalidSecret
    case nameTooLong

    var errorDescription: String? {
        switch self {
        case .missingPrefix:
            return "This is not a HealthBridge pairing code. It must start with “HB1:”."
        case .tooLong:
            return "The pairing code is too long."
        case .invalidEncoding:
            return "The pairing code is damaged (invalid encoding). Copy it again from your agent."
        case .invalidJSON:
            return "The pairing code is damaged (invalid content). Copy it again from your agent."
        case .unsupportedVersion:
            return "This pairing code was created by an unsupported version. Update HealthBridge and your agent."
        case .invalidRelayURL:
            return "The relay address in the pairing code is invalid."
        case .insecureRelayURL:
            return "The relay must use https. Plain http is only allowed for localhost or private networks."
        case .invalidAccount:
            return "The account ID in the pairing code is invalid."
        case .invalidSigningKey:
            return "The agent's signing key in the pairing code is invalid."
        case .invalidEncryptionKey:
            return "The agent's encryption key in the pairing code is invalid."
        case .invalidSecret:
            return "The pairing secret in the pairing code is invalid."
        case .nameTooLong:
            return "The agent name in the pairing code is too long."
        }
    }
}

enum PairingCode {
    static let maxLength = 4096

    private struct RawPayload: Decodable {
        let v: Int
        let relay: String
        let account: String
        let sig: String
        let enc: String
        let secret: String
        let name: String?
    }

    /// Decodes and validates a pairing code exactly like the reference implementation.
    static func decode(_ input: String) throws -> PairingPayload {
        let code = input.trimmingCharacters(in: .whitespacesAndNewlines)
        guard code.hasPrefix(HB1.pairingPrefix) else {
            throw PairingCodeError.missingPrefix
        }
        guard code.utf8.count <= maxLength else {
            throw PairingCodeError.tooLong
        }
        let encoded = String(code.dropFirst(HB1.pairingPrefix.count))
        guard let json = B64URL.decode(encoded) else {
            throw PairingCodeError.invalidEncoding
        }
        let raw: RawPayload
        do {
            raw = try JSONDecoder().decode(RawPayload.self, from: json)
        } catch {
            throw PairingCodeError.invalidJSON
        }
        guard raw.v == HB1.version else {
            throw PairingCodeError.unsupportedVersion
        }
        _ = try RelayURLValidator.validate(raw.relay)
        guard HB1.isValidAccountID(raw.account) else {
            throw PairingCodeError.invalidAccount
        }
        guard let signingKey = B64.decode(raw.sig, length: 32),
              (try? Curve25519.Signing.PublicKey(rawRepresentation: signingKey)) != nil else {
            throw PairingCodeError.invalidSigningKey
        }
        guard let encryptionKey = B64.decode(raw.enc, length: 32),
              (try? Curve25519.KeyAgreement.PublicKey(rawRepresentation: encryptionKey)) != nil else {
            throw PairingCodeError.invalidEncryptionKey
        }
        guard B64URL.decode(raw.secret, length: 32) != nil else {
            throw PairingCodeError.invalidSecret
        }
        let rawName = raw.name ?? ""
        guard rawName.unicodeScalars.count <= 64 else {
            throw PairingCodeError.nameTooLong
        }
        let cleanedName = HB1.cleanLabel(rawName)

        return PairingPayload(
            relay: raw.relay,
            account: raw.account,
            agentSigningKey: signingKey,
            agentEncryptionKey: encryptionKey,
            secret: raw.secret,
            name: cleanedName.isEmpty ? nil : cleanedName,
            safetyCode: HB1.safetyCode(
                account: raw.account,
                signingPublicKey: signingKey,
                encryptionPublicKey: encryptionKey
            ),
            keyID: HB1.keyID(encryptionPublicKey: encryptionKey)
        )
    }
}

/// Relay URL rule from docs/PROTOCOL.md §3: `https://…`, or plain `http://` only for
/// `localhost`, loopback or private-network IP addresses (development).
enum RelayURLValidator {
    static func validate(_ raw: String) throws -> URL {
        guard raw.utf8.count <= 512 else {
            throw PairingCodeError.invalidRelayURL
        }
        guard let components = URLComponents(string: raw),
              let scheme = components.scheme?.lowercased(),
              let host = components.host, !host.isEmpty,
              components.user == nil,
              components.password == nil,
              components.query == nil,
              components.fragment == nil,
              let url = components.url else {
            throw PairingCodeError.invalidRelayURL
        }
        switch scheme {
        case "https":
            return url
        case "http":
            let bareHost = host.trimmingCharacters(in: CharacterSet(charactersIn: "[]"))
            if bareHost == "localhost" || IPAddress.isLoopbackOrPrivate(bareHost) {
                return url
            }
            throw PairingCodeError.insecureRelayURL
        default:
            throw PairingCodeError.insecureRelayURL
        }
    }
}

/// Minimal IP address classification mirroring Go's `net.IP.IsLoopback()` / `IsPrivate()`.
enum IPAddress {
    static func isLoopbackOrPrivate(_ host: String) -> Bool {
        if let v4 = ipv4Bytes(host) {
            return isLoopbackOrPrivateV4(v4)
        }
        if let v6 = ipv6Bytes(host) {
            // ::1
            if v6[0..<15].allSatisfy({ $0 == 0 }) && v6[15] == 1 {
                return true
            }
            // fc00::/7 (unique local addresses)
            if v6[0] & 0xfe == 0xfc {
                return true
            }
            // IPv4-mapped ::ffff:a.b.c.d
            if v6[0..<10].allSatisfy({ $0 == 0 }) && v6[10] == 0xff && v6[11] == 0xff {
                return isLoopbackOrPrivateV4(Array(v6[12..<16]))
            }
        }
        return false
    }

    private static func isLoopbackOrPrivateV4(_ bytes: [UInt8]) -> Bool {
        guard bytes.count == 4 else { return false }
        return bytes[0] == 127                                  // 127.0.0.0/8
            || bytes[0] == 10                                   // 10.0.0.0/8
            || (bytes[0] == 172 && (bytes[1] & 0xf0) == 16)     // 172.16.0.0/12
            || (bytes[0] == 192 && bytes[1] == 168)             // 192.168.0.0/16
    }

    static func ipv4Bytes(_ host: String) -> [UInt8]? {
        var address = in_addr()
        let result = host.withCString { inet_pton(AF_INET, $0, &address) }
        guard result == 1 else { return nil }
        return withUnsafeBytes(of: address) { Array($0) }
    }

    static func ipv6Bytes(_ host: String) -> [UInt8]? {
        var address = in6_addr()
        let result = host.withCString { inet_pton(AF_INET6, $0, &address) }
        guard result == 1 else { return nil }
        return withUnsafeBytes(of: address) { Array($0) }
    }
}
