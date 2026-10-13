import Foundation
import Security

/// Standard Base64 **with** padding (RFC 4648 §4), called `b64` in docs/PROTOCOL.md.
enum B64 {
    static func encode(_ data: Data) -> String {
        data.base64EncodedString()
    }

    /// Strict decoding: rejects non-canonical input (for example non-zero padding bits)
    /// and, if `length` is given, any other decoded length.
    static func decode(_ string: String, length: Int? = nil) -> Data? {
        guard let data = Data(base64Encoded: string), data.base64EncodedString() == string else {
            return nil
        }
        if let length, data.count != length {
            return nil
        }
        return data
    }
}

/// Base64url **without** padding (RFC 4648 §5), called `b64u` in docs/PROTOCOL.md.
enum B64URL {
    static func encode(_ data: Data) -> String {
        var string = data.base64EncodedString()
            .replacingOccurrences(of: "+", with: "-")
            .replacingOccurrences(of: "/", with: "_")
        while string.hasSuffix("=") {
            string.removeLast()
        }
        return string
    }

    /// Strict decoding: only the URL-safe alphabet, no padding, canonical trailing bits.
    static func decode(_ string: String, length: Int? = nil) -> Data? {
        let utf8 = Array(string.utf8)
        guard utf8.allSatisfy(isAlphabet), utf8.count % 4 != 1 else {
            return nil
        }
        var standard = string
            .replacingOccurrences(of: "-", with: "+")
            .replacingOccurrences(of: "_", with: "/")
        let remainder = utf8.count % 4
        if remainder != 0 {
            standard += String(repeating: "=", count: 4 - remainder)
        }
        guard let data = Data(base64Encoded: standard), encode(data) == string else {
            return nil
        }
        if let length, data.count != length {
            return nil
        }
        return data
    }

    static func isAlphabet(_ byte: UInt8) -> Bool {
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

extension Data {
    /// Lowercase hexadecimal representation (`hex` in docs/PROTOCOL.md).
    var hexString: String {
        map { String(format: "%02x", $0) }.joined()
    }
}

enum SecureRandom {
    /// Cryptographically secure random bytes.
    static func bytes(_ count: Int) -> Data {
        var bytes = [UInt8](repeating: 0, count: count)
        let status = SecRandomCopyBytes(kSecRandomDefault, count, &bytes)
        if status != errSecSuccess {
            // SystemRandomNumberGenerator is also a CSPRNG on Apple platforms.
            var generator = SystemRandomNumberGenerator()
            for index in bytes.indices {
                bytes[index] = UInt8.random(in: UInt8.min...UInt8.max, using: &generator)
            }
        }
        return Data(bytes)
    }
}

/// RFC 3339 timestamps, UTC, second precision, `Z` suffix.
enum RFC3339 {
    private static let formatter: ISO8601DateFormatter = {
        let formatter = ISO8601DateFormatter()
        formatter.formatOptions = [.withInternetDateTime]
        formatter.timeZone = TimeZone(secondsFromGMT: 0)
        return formatter
    }()

    private static let fractionalFormatter: ISO8601DateFormatter = {
        let formatter = ISO8601DateFormatter()
        formatter.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        formatter.timeZone = TimeZone(secondsFromGMT: 0)
        return formatter
    }()

    static func string(from date: Date) -> String {
        formatter.string(from: date)
    }

    static func date(from string: String) -> Date? {
        formatter.date(from: string) ?? fractionalFormatter.date(from: string)
    }
}
