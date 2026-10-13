import CryptoKit
import Foundation

enum EnvelopeKind: String, Codable, Sendable {
    case sync
    case backfill
}

enum EnvelopeCompression: String, Codable, Sendable {
    case none
    case deflate
}

/// One encrypted HealthBatch as uploaded to the relay (docs/PROTOCOL.md §6).
struct Envelope: Codable, Equatable, Sendable {
    let id: String
    let account: String
    let kind: String
    let alg: String
    let kid: String
    /// b64, 32 bytes
    let epk: String
    /// b64, 12 bytes
    let nonce: String
    let compression: String
    let createdAt: String
    /// b64(ciphertext ‖ 16-byte Poly1305 tag)
    let ct: String

    enum CodingKeys: String, CodingKey {
        case id, account, kind, alg, kid, epk, nonce, compression
        case createdAt = "created_at"
        case ct
    }

    var aad: Data {
        EnvelopeCrypto.aad(account: account, id: id, kind: kind, compression: compression, createdAt: createdAt)
    }

    /// Approximate size of the ciphertext in bytes (for the activity log).
    var ciphertextSize: Int {
        ct.utf8.count / 4 * 3
    }
}

enum EnvelopeError: LocalizedError, Equatable {
    case invalidRecipientKey
    case invalidNonce
    case compressionFailed
    case decompressionFailed
    case plaintextTooLarge
    case malformedEnvelope
    case unsupportedAlgorithm
    case wrongRecipient
    case authenticationFailed

    var errorDescription: String? {
        switch self {
        case .invalidRecipientKey: return "The agent's encryption key is invalid. Pair again."
        case .invalidNonce: return "Could not create a nonce for encryption."
        case .compressionFailed: return "Compressing the health data failed."
        case .decompressionFailed: return "Decompressing the data failed."
        case .plaintextTooLarge: return "The data is too large for one envelope."
        case .malformedEnvelope: return "The envelope is malformed."
        case .unsupportedAlgorithm: return "The envelope uses an unsupported algorithm."
        case .wrongRecipient: return "The envelope is addressed to a different key or account."
        case .authenticationFailed: return "The envelope could not be authenticated."
        }
    }
}

/// X25519 + HKDF-SHA256 + ChaCha20-Poly1305 envelope encryption.
enum EnvelopeCrypto {
    /// The receiver rejects plaintexts above 64 MiB after decompression.
    static let maxPlaintextSize = 64 * 1024 * 1024
    static let tagSize = 16

    /// `UTF8("HB1-ENVELOPE\n" + account + "\n" + id + "\n" + kind + "\n" + compression + "\n" + created_at)`
    static func aad(account: String, id: String, kind: String, compression: String, createdAt: String) -> Data {
        Data([HB1.envelopeAADPrefix, account, id, kind, compression, createdAt].joined(separator: "\n").utf8)
    }

    /// `HKDF-SHA256(ikm = shared, salt = e_pub ‖ enc_pub, info = "HB1-ENVELOPE-KEY", L = 32)`
    static func deriveKey(sharedSecret: SharedSecret, ephemeralPublicKey: Data, recipientPublicKey: Data) -> SymmetricKey {
        sharedSecret.hkdfDerivedSymmetricKey(
            using: SHA256.self,
            salt: ephemeralPublicKey + recipientPublicKey,
            sharedInfo: Data(HB1.envelopeKeyInfo.utf8),
            outputByteCount: 32
        )
    }

    /// Seals `plaintext` for the agent. `ephemeralKey` and `nonce` are injectable only for
    /// deterministic test vectors; production code always uses fresh randomness.
    static func seal(
        _ plaintext: Data,
        recipient: Curve25519.KeyAgreement.PublicKey,
        account: String,
        kind: EnvelopeKind,
        compression: EnvelopeCompression = .deflate,
        id: String = UUID().uuidString.lowercased(),
        createdAt: Date = Date(),
        ephemeralKey: Curve25519.KeyAgreement.PrivateKey = Curve25519.KeyAgreement.PrivateKey(),
        nonce nonceBytes: Data? = nil
    ) throws -> Envelope {
        guard plaintext.count <= maxPlaintextSize else {
            throw EnvelopeError.plaintextTooLarge
        }
        let recipientPublicKey = recipient.rawRepresentation
        let ephemeralPublicKey = ephemeralKey.publicKey.rawRepresentation

        let sharedSecret: SharedSecret
        do {
            sharedSecret = try ephemeralKey.sharedSecretFromKeyAgreement(with: recipient)
        } catch {
            throw EnvelopeError.invalidRecipientKey
        }
        let key = deriveKey(
            sharedSecret: sharedSecret,
            ephemeralPublicKey: ephemeralPublicKey,
            recipientPublicKey: recipientPublicKey
        )

        let createdAtString = RFC3339.string(from: createdAt)
        let associatedData = Self.aad(
            account: account,
            id: id,
            kind: kind.rawValue,
            compression: compression.rawValue,
            createdAt: createdAtString
        )

        let body: Data
        switch compression {
        case .none:
            body = plaintext
        case .deflate:
            body = try rawDeflate(plaintext)
        }

        let nonceData = nonceBytes ?? SecureRandom.bytes(12)
        let nonce: ChaChaPoly.Nonce
        do {
            nonce = try ChaChaPoly.Nonce(data: nonceData)
        } catch {
            throw EnvelopeError.invalidNonce
        }
        let sealedBox = try ChaChaPoly.seal(body, using: key, nonce: nonce, authenticating: associatedData)
        // Wire format is ciphertext ‖ tag (NOT `combined`, which would prepend the nonce).
        let ciphertext = sealedBox.ciphertext + sealedBox.tag

        return Envelope(
            id: id,
            account: account,
            kind: kind.rawValue,
            alg: HB1.envelopeAlgorithm,
            kid: HB1.keyID(encryptionPublicKey: recipientPublicKey),
            epk: B64.encode(ephemeralPublicKey),
            nonce: B64.encode(nonceData),
            compression: compression.rawValue,
            createdAt: createdAtString,
            ct: B64.encode(ciphertext)
        )
    }

    /// Opens an envelope with the agent's private key. The app never holds that key; this
    /// exists for the unit tests and mirrors the agent's checks.
    static func open(
        _ envelope: Envelope,
        account: String,
        recipientPrivateKey: Curve25519.KeyAgreement.PrivateKey
    ) throws -> Data {
        guard envelope.alg == HB1.envelopeAlgorithm else {
            throw EnvelopeError.unsupportedAlgorithm
        }
        let recipientPublicKey = recipientPrivateKey.publicKey.rawRepresentation
        guard envelope.account == account,
              envelope.kid == HB1.keyID(encryptionPublicKey: recipientPublicKey) else {
            throw EnvelopeError.wrongRecipient
        }
        guard let compression = EnvelopeCompression(rawValue: envelope.compression),
              EnvelopeKind(rawValue: envelope.kind) != nil,
              let epk = B64.decode(envelope.epk, length: 32),
              let nonceData = B64.decode(envelope.nonce, length: 12),
              let ciphertext = B64.decode(envelope.ct),
              ciphertext.count >= tagSize else {
            throw EnvelopeError.malformedEnvelope
        }
        let ephemeralPublicKey: Curve25519.KeyAgreement.PublicKey
        let sharedSecret: SharedSecret
        do {
            ephemeralPublicKey = try Curve25519.KeyAgreement.PublicKey(rawRepresentation: epk)
            sharedSecret = try recipientPrivateKey.sharedSecretFromKeyAgreement(with: ephemeralPublicKey)
        } catch {
            throw EnvelopeError.malformedEnvelope
        }
        let key = deriveKey(sharedSecret: sharedSecret, ephemeralPublicKey: epk, recipientPublicKey: recipientPublicKey)

        let body: Data
        do {
            let sealedBox = try ChaChaPoly.SealedBox(
                nonce: ChaChaPoly.Nonce(data: nonceData),
                ciphertext: ciphertext.prefix(ciphertext.count - tagSize),
                tag: ciphertext.suffix(tagSize)
            )
            body = try ChaChaPoly.open(sealedBox, using: key, authenticating: envelope.aad)
        } catch {
            throw EnvelopeError.authenticationFailed
        }

        switch compression {
        case .none:
            guard body.count <= maxPlaintextSize else { throw EnvelopeError.plaintextTooLarge }
            return body
        case .deflate:
            let plaintext = try rawInflate(body)
            guard plaintext.count <= maxPlaintextSize else { throw EnvelopeError.plaintextTooLarge }
            return plaintext
        }
    }

    /// Raw DEFLATE (RFC 1951, no zlib header), as produced by Apple's `COMPRESSION_ZLIB`.
    static func rawDeflate(_ data: Data) throws -> Data {
        do {
            return try (data as NSData).compressed(using: .zlib) as Data
        } catch {
            throw EnvelopeError.compressionFailed
        }
    }

    static func rawInflate(_ data: Data) throws -> Data {
        do {
            return try (data as NSData).decompressed(using: .zlib) as Data
        } catch {
            throw EnvelopeError.decompressionFailed
        }
    }
}
