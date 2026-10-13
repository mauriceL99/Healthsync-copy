import CryptoKit
import XCTest
@testable import HealthBridge

/// Checks the iOS implementation against `testdata/protocol_vectors.json`
/// (copied byte-for-byte to `HealthBridgeTests/Resources/protocol_vectors.json`).
final class ProtocolVectorTests: XCTestCase {
    // MARK: - Vector file

    struct Vectors: Decodable {
        struct Agent: Decodable {
            let account: String
            let name: String
            let relay: String
            let sigSeed: String
            let sigPub: String
            let encPriv: String
            let encPub: String
            let secret: String
            let kid: String
            let safetyCode: String
            let pairingHash: String
            let pairingCode: String

            enum CodingKeys: String, CodingKey {
                case account, name, relay, kid
                case sigSeed = "sig_seed_b64"
                case sigPub = "sig_pub_b64"
                case encPriv = "enc_priv_b64"
                case encPub = "enc_pub_b64"
                case secret = "secret_b64u"
                case safetyCode = "safety_code"
                case pairingHash = "pairing_hash"
                case pairingCode = "pairing_code"
            }
        }

        struct Device: Decodable {
            let sigSeed: String
            let sigPub: String

            enum CodingKeys: String, CodingKey {
                case sigSeed = "sig_seed_b64"
                case sigPub = "sig_pub_b64"
            }
        }

        struct Request: Decodable {
            let method: String
            let path: String
            let key: String
            let timestamp: String
            let nonce: String
            let body: String
            let bodySHA256: String
            let stringToSign: String
            let signature: String

            enum CodingKeys: String, CodingKey {
                case method, path, key, timestamp, nonce, body
                case bodySHA256 = "body_sha256"
                case stringToSign = "string_to_sign"
                case signature = "signature_b64"
            }
        }

        struct EnvelopeVector: Decodable {
            let plaintext: String
            let ephemeralPriv: String
            let sharedSecret: String
            let key: String
            let aad: String
            let envelope: Envelope

            enum CodingKeys: String, CodingKey {
                case plaintext, aad, envelope
                case ephemeralPriv = "ephemeral_priv_b64"
                case sharedSecret = "shared_secret_b64"
                case key = "key_b64"
            }
        }

        let agent: Agent
        let device: Device
        let request: Request
        let envelopes: [EnvelopeVector]
    }

    private var vectors: Vectors!

    override func setUpWithError() throws {
        let bundle = Bundle(for: ProtocolVectorTests.self)
        let url = try XCTUnwrap(
            bundle.url(forResource: "protocol_vectors", withExtension: "json"),
            "protocol_vectors.json missing from the test bundle"
        )
        vectors = try JSONDecoder().decode(Vectors.self, from: Data(contentsOf: url))
    }

    private func b64(_ string: String, length: Int? = nil, file: StaticString = #filePath, line: UInt = #line) throws -> Data {
        try XCTUnwrap(B64.decode(string, length: length), "invalid b64 in vector: \(string)", file: file, line: line)
    }

    private var agentEncryptionPrivateKey: Curve25519.KeyAgreement.PrivateKey {
        get throws {
            try Curve25519.KeyAgreement.PrivateKey(rawRepresentation: b64(vectors.agent.encPriv, length: 32))
        }
    }

    private var agentEncryptionPublicKey: Curve25519.KeyAgreement.PublicKey {
        get throws {
            try Curve25519.KeyAgreement.PublicKey(rawRepresentation: b64(vectors.agent.encPub, length: 32))
        }
    }

    // MARK: - Identities

    func testKeysDeriveFromSeeds() throws {
        let agentSig = try Curve25519.Signing.PrivateKey(rawRepresentation: b64(vectors.agent.sigSeed, length: 32))
        XCTAssertEqual(B64.encode(agentSig.publicKey.rawRepresentation), vectors.agent.sigPub)

        XCTAssertEqual(B64.encode(try agentEncryptionPrivateKey.publicKey.rawRepresentation), vectors.agent.encPub)

        let deviceSig = try Curve25519.Signing.PrivateKey(rawRepresentation: b64(vectors.device.sigSeed, length: 32))
        XCTAssertEqual(B64.encode(deviceSig.publicKey.rawRepresentation), vectors.device.sigPub)
    }

    func testKeyID() throws {
        XCTAssertEqual(HB1.keyID(encryptionPublicKey: try b64(vectors.agent.encPub, length: 32)), vectors.agent.kid)
        XCTAssertEqual(vectors.agent.kid.count, 16)
    }

    func testSafetyCode() throws {
        let code = HB1.safetyCode(
            account: vectors.agent.account,
            signingPublicKey: try b64(vectors.agent.sigPub, length: 32),
            encryptionPublicKey: try b64(vectors.agent.encPub, length: 32)
        )
        XCTAssertEqual(code, vectors.agent.safetyCode)
    }

    func testPairingHash() throws {
        let secret = try XCTUnwrap(B64URL.decode(vectors.agent.secret, length: 32))
        XCTAssertEqual(HB1.pairingHash(secret: secret), vectors.agent.pairingHash)
    }

    func testAccountAndDeviceIDPatterns() {
        XCTAssertTrue(HB1.isValidAccountID(vectors.agent.account))
        XCTAssertTrue(HB1.isValidDeviceID(String(vectors.request.key.dropFirst("device:".count))))
        XCTAssertFalse(HB1.isValidAccountID("hb_short"))
        XCTAssertFalse(HB1.isValidAccountID("xx_oKGio6SlpqeoqaqrrK2urw"))
        XCTAssertFalse(HB1.isValidAccountID("hb_oKGio6SlpqeoqaqrrK2ur+"))
    }

    // MARK: - Pairing code

    func testPairingCodeDecodes() throws {
        let payload = try PairingCode.decode(vectors.agent.pairingCode)
        XCTAssertEqual(payload.relay, vectors.agent.relay)
        XCTAssertEqual(payload.account, vectors.agent.account)
        XCTAssertEqual(payload.agentSigningKey, try b64(vectors.agent.sigPub))
        XCTAssertEqual(payload.agentEncryptionKey, try b64(vectors.agent.encPub))
        XCTAssertEqual(payload.secret, vectors.agent.secret)
        XCTAssertEqual(payload.name, vectors.agent.name)
        XCTAssertEqual(payload.safetyCode, vectors.agent.safetyCode)
        XCTAssertEqual(payload.keyID, vectors.agent.kid)
        XCTAssertEqual(payload.relayHost, "relay.example.com")
    }

    func testPairingCodeToleratesSurroundingWhitespace() throws {
        let payload = try PairingCode.decode("  \n" + vectors.agent.pairingCode + "\n ")
        XCTAssertEqual(payload.safetyCode, vectors.agent.safetyCode)
    }

    // MARK: - Signed requests

    func testStringToSign() throws {
        let body = Data(vectors.request.body.utf8)
        XCTAssertEqual(Data(SHA256.hash(data: body)).hexString, vectors.request.bodySHA256)
        let stringToSign = RequestSigner.stringToSign(
            method: vectors.request.method,
            pathAndQuery: vectors.request.path,
            key: vectors.request.key,
            timestamp: vectors.request.timestamp,
            nonce: vectors.request.nonce,
            body: body
        )
        XCTAssertEqual(stringToSign, vectors.request.stringToSign)
        XCTAssertFalse(stringToSign.hasSuffix("\n"))
    }

    func testEmptyBodyHashesEmptyString() {
        let stringToSign = RequestSigner.stringToSign(
            method: "get", pathAndQuery: "/v1/devices/self", key: "device:x", timestamp: "1", nonce: "n", body: Data()
        )
        XCTAssertTrue(stringToSign.hasPrefix("HB1-REQUEST\nGET\n/v1/devices/self\n"))
        XCTAssertTrue(stringToSign.hasSuffix("e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"))
    }

    func testVectorSignatureVerifiesWithDeviceKey() throws {
        let devicePublicKey = try Curve25519.Signing.PublicKey(rawRepresentation: b64(vectors.device.sigPub, length: 32))
        let signature = try b64(vectors.request.signature, length: 64)
        XCTAssertTrue(devicePublicKey.isValidSignature(signature, for: Data(vectors.request.stringToSign.utf8)))
        XCTAssertFalse(devicePublicKey.isValidSignature(signature, for: Data((vectors.request.stringToSign + "x").utf8)))
    }

    func testSignerProducesVerifiableHeaders() throws {
        let deviceKey = try Curve25519.Signing.PrivateKey(rawRepresentation: b64(vectors.device.sigSeed, length: 32))
        let headers = try RequestSigner.sign(
            privateKey: deviceKey,
            method: vectors.request.method,
            pathAndQuery: vectors.request.path,
            key: vectors.request.key,
            body: Data(vectors.request.body.utf8),
            now: Date(timeIntervalSince1970: 1_760_000_000),
            nonce: vectors.request.nonce
        )
        XCTAssertEqual(headers.key, vectors.request.key)
        XCTAssertEqual(headers.timestamp, vectors.request.timestamp)
        XCTAssertEqual(headers.nonce, vectors.request.nonce)
        // CryptoKit may randomize Ed25519 signatures, so verify instead of comparing bytes.
        let signature = try b64(headers.signature, length: 64)
        XCTAssertTrue(deviceKey.publicKey.isValidSignature(signature, for: Data(vectors.request.stringToSign.utf8)))
    }

    func testNonceFormat() {
        let nonce = RequestSigner.makeNonce()
        XCTAssertEqual(nonce.count, 22)
        XCTAssertNotNil(B64URL.decode(nonce, length: 16))
        XCTAssertNotEqual(nonce, RequestSigner.makeNonce())
    }

    // MARK: - Envelopes

    func testEnvelopeKeyDerivationMatchesVectors() throws {
        let recipient = try agentEncryptionPublicKey
        for vector in vectors.envelopes {
            let ephemeral = try Curve25519.KeyAgreement.PrivateKey(rawRepresentation: b64(vector.ephemeralPriv, length: 32))
            XCTAssertEqual(B64.encode(ephemeral.publicKey.rawRepresentation), vector.envelope.epk)

            let shared = try ephemeral.sharedSecretFromKeyAgreement(with: recipient)
            XCTAssertEqual(shared.withUnsafeBytes { Data($0) }, try b64(vector.sharedSecret, length: 32))

            let key = EnvelopeCrypto.deriveKey(
                sharedSecret: shared,
                ephemeralPublicKey: ephemeral.publicKey.rawRepresentation,
                recipientPublicKey: recipient.rawRepresentation
            )
            XCTAssertEqual(key.withUnsafeBytes { Data($0) }, try b64(vector.key, length: 32))

            let aad = EnvelopeCrypto.aad(
                account: vector.envelope.account,
                id: vector.envelope.id,
                kind: vector.envelope.kind,
                compression: vector.envelope.compression,
                createdAt: vector.envelope.createdAt
            )
            XCTAssertEqual(aad, Data(vector.aad.utf8))
            XCTAssertEqual(vector.envelope.aad, Data(vector.aad.utf8))
            XCTAssertEqual(vector.envelope.kid, vectors.agent.kid)
        }
    }

    func testResealUncompressedVectorIsByteExact() throws {
        let vector = try XCTUnwrap(vectors.envelopes.first { $0.envelope.compression == "none" })
        let ephemeral = try Curve25519.KeyAgreement.PrivateKey(rawRepresentation: b64(vector.ephemeralPriv, length: 32))
        let createdAt = try XCTUnwrap(RFC3339.date(from: vector.envelope.createdAt))

        let sealed = try EnvelopeCrypto.seal(
            Data(vector.plaintext.utf8),
            recipient: try agentEncryptionPublicKey,
            account: vectors.agent.account,
            kind: .sync,
            compression: .none,
            id: vector.envelope.id,
            createdAt: createdAt,
            ephemeralKey: ephemeral,
            nonce: try b64(vector.envelope.nonce, length: 12)
        )
        XCTAssertEqual(sealed.ct, vector.envelope.ct)
        XCTAssertEqual(sealed.createdAt, vector.envelope.createdAt)
        XCTAssertEqual(sealed, vector.envelope)
    }

    func testOpenBothVectors() throws {
        let privateKey = try agentEncryptionPrivateKey
        XCTAssertEqual(vectors.envelopes.count, 2)
        for vector in vectors.envelopes {
            let plaintext = try EnvelopeCrypto.open(vector.envelope, account: vectors.agent.account, recipientPrivateKey: privateKey)
            XCTAssertEqual(String(decoding: plaintext, as: UTF8.self), vector.plaintext)
        }
    }

    func testInflateDeflateVectorManually() throws {
        // Decrypt the deflate vector by hand and inflate with NSData (raw DEFLATE).
        let vector = try XCTUnwrap(vectors.envelopes.first { $0.envelope.compression == "deflate" })
        let key = SymmetricKey(data: try b64(vector.key, length: 32))
        let ciphertext = try b64(vector.envelope.ct)
        let box = try ChaChaPoly.SealedBox(
            nonce: ChaChaPoly.Nonce(data: b64(vector.envelope.nonce, length: 12)),
            ciphertext: ciphertext.prefix(ciphertext.count - 16),
            tag: ciphertext.suffix(16)
        )
        let compressed = try ChaChaPoly.open(box, using: key, authenticating: Data(vector.aad.utf8))
        let plaintext = try (compressed as NSData).decompressed(using: .zlib) as Data
        XCTAssertEqual(String(decoding: plaintext, as: UTF8.self), vector.plaintext)
    }

    func testFreshDeflateEnvelopeRoundTrips() throws {
        let plaintext = Data(String(repeating: vectors.envelopes[0].plaintext, count: 20).utf8)
        let envelope = try EnvelopeCrypto.seal(
            plaintext,
            recipient: try agentEncryptionPublicKey,
            account: vectors.agent.account,
            kind: .backfill
        )
        XCTAssertEqual(envelope.alg, "HB1-X25519-HKDF-SHA256-CHACHA20POLY1305")
        XCTAssertEqual(envelope.compression, "deflate")
        XCTAssertEqual(envelope.kind, "backfill")
        XCTAssertEqual(envelope.kid, vectors.agent.kid)
        XCTAssertEqual(envelope.id, envelope.id.lowercased())
        XCTAssertNotNil(UUID(uuidString: envelope.id))
        XCTAssertNotNil(B64.decode(envelope.epk, length: 32))
        XCTAssertNotNil(B64.decode(envelope.nonce, length: 12))
        XCTAssertNotNil(envelope.createdAt.range(
            of: #"^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z$"#,
            options: .regularExpression
        ))
        // Compression must actually shrink repetitive JSON.
        XCTAssertLessThan(try XCTUnwrap(B64.decode(envelope.ct)).count, plaintext.count)

        let opened = try EnvelopeCrypto.open(envelope, account: vectors.agent.account, recipientPrivateKey: try agentEncryptionPrivateKey)
        XCTAssertEqual(opened, plaintext)
    }

    func testTamperedEnvelopeIsRejected() throws {
        let original = vectors.envelopes[0].envelope
        let tampered = Envelope(
            id: original.id,
            account: original.account,
            kind: "backfill",
            alg: original.alg,
            kid: original.kid,
            epk: original.epk,
            nonce: original.nonce,
            compression: original.compression,
            createdAt: original.createdAt,
            ct: original.ct
        )
        XCTAssertThrowsError(
            try EnvelopeCrypto.open(tampered, account: vectors.agent.account, recipientPrivateKey: try agentEncryptionPrivateKey)
        ) { error in
            XCTAssertEqual(error as? EnvelopeError, .authenticationFailed)
        }
        XCTAssertThrowsError(
            try EnvelopeCrypto.open(original, account: "hb_AAAAAAAAAAAAAAAAAAAAAA", recipientPrivateKey: try agentEncryptionPrivateKey)
        ) { error in
            XCTAssertEqual(error as? EnvelopeError, .wrongRecipient)
        }
    }

    func testEnvelopeJSONUsesProtocolKeys() throws {
        let data = try RelayClient.encoder.encode(UploadEnvelopesRequest(envelopes: [vectors.envelopes[0].envelope]))
        let object = try XCTUnwrap(JSONSerialization.jsonObject(with: data) as? [String: Any])
        let envelopes = try XCTUnwrap(object["envelopes"] as? [[String: Any]])
        XCTAssertEqual(
            Set(try XCTUnwrap(envelopes.first).keys),
            ["id", "account", "kind", "alg", "kid", "epk", "nonce", "compression", "created_at", "ct"]
        )
        XCTAssertFalse(String(decoding: data, as: UTF8.self).contains("\\/"), "slashes must not be escaped")
    }
}
