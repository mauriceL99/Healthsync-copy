import CryptoKit
import Foundation

/// The four authentication headers of a signed relay request (docs/PROTOCOL.md §4).
struct SignedHeaders: Equatable, Sendable {
    static let keyHeader = "HB-Key"
    static let timestampHeader = "HB-Timestamp"
    static let nonceHeader = "HB-Nonce"
    static let signatureHeader = "HB-Signature"

    let key: String
    let timestamp: String
    let nonce: String
    let signature: String

    func apply(to request: inout URLRequest) {
        request.setValue(key, forHTTPHeaderField: Self.keyHeader)
        request.setValue(timestamp, forHTTPHeaderField: Self.timestampHeader)
        request.setValue(nonce, forHTTPHeaderField: Self.nonceHeader)
        request.setValue(signature, forHTTPHeaderField: Self.signatureHeader)
    }
}

/// HB-Key values.
enum RelayKey {
    static let newDevice = "new-device"

    static func device(_ deviceID: String) -> String {
        "device:" + deviceID
    }
}

enum RequestSigner {
    /// Builds the canonical string to sign:
    /// ```
    /// HB1-REQUEST\n<METHOD>\n<path?query>\n<HB-Key>\n<timestamp>\n<nonce>\n<hex(SHA256(body))>
    /// ```
    static func stringToSign(
        method: String,
        pathAndQuery: String,
        key: String,
        timestamp: String,
        nonce: String,
        body: Data
    ) -> String {
        [
            HB1.requestSignaturePrefix,
            method.uppercased(),
            pathAndQuery,
            key,
            timestamp,
            nonce,
            Data(SHA256.hash(data: body)).hexString,
        ].joined(separator: "\n")
    }

    /// A fresh request nonce: `b64u(16 random bytes)`.
    static func makeNonce() -> String {
        B64URL.encode(SecureRandom.bytes(16))
    }

    /// Unix time in whole seconds, decimal.
    static func timestamp(for date: Date) -> String {
        String(Int64(date.timeIntervalSince1970.rounded(.down)))
    }

    static func sign(
        privateKey: Curve25519.Signing.PrivateKey,
        method: String,
        pathAndQuery: String,
        key: String,
        body: Data,
        now: Date = Date(),
        nonce: String = RequestSigner.makeNonce()
    ) throws -> SignedHeaders {
        let unixTime = Self.timestamp(for: now)
        let message = stringToSign(
            method: method,
            pathAndQuery: pathAndQuery,
            key: key,
            timestamp: unixTime,
            nonce: nonce,
            body: body
        )
        let signature = try privateKey.signature(for: Data(message.utf8))
        return SignedHeaders(key: key, timestamp: unixTime, nonce: nonce, signature: B64.encode(signature))
    }
}
