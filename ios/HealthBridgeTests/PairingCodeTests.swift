import XCTest
@testable import HealthBridge

/// Validation rules for pairing codes (docs/PROTOCOL.md §3), mirroring the Go reference.
final class PairingCodeTests: XCTestCase {
    private let account = "hb_oKGio6SlpqeoqaqrrK2urw"
    private let sig = "ebVWLo/mVPlAeLES6KmLp5AfhTrmlb7X4OORC60ElmQ="
    private let enc = "eaYx7t4b+cmPEgMs3q3Q56B5OY/HhriMyEbsia+FpRo="
    private let secret = "wMHCw8TFxsfIycrLzM3Oz9DR0tPU1dbX2Nna29zd3t8"

    private func makeCode(
        version: Int = 1,
        relay: String = "https://relay.example.com",
        account: String? = nil,
        sig: String? = nil,
        enc: String? = nil,
        secret: String? = nil,
        name: String? = nil
    ) throws -> String {
        var object: [String: Any] = [
            "v": version,
            "relay": relay,
            "account": account ?? self.account,
            "sig": sig ?? self.sig,
            "enc": enc ?? self.enc,
            "secret": secret ?? self.secret,
        ]
        if let name {
            object["name"] = name
        }
        let json = try JSONSerialization.data(withJSONObject: object)
        return HB1.pairingPrefix + B64URL.encode(json)
    }

    private func assertRejects(
        _ code: String,
        with expected: PairingCodeError,
        file: StaticString = #filePath,
        line: UInt = #line
    ) {
        XCTAssertThrowsError(try PairingCode.decode(code), file: file, line: line) { error in
            XCTAssertEqual(error as? PairingCodeError, expected, file: file, line: line)
        }
    }

    // MARK: - Relay URL rule

    func testAcceptedRelayURLs() throws {
        let accepted = [
            "https://relay.example.com",
            "https://relay.example.com/",
            "https://relay.example.com:8443/prefix",
            "http://localhost:8787",
            "http://127.0.0.1:8787",
            "http://10.0.0.7:8787",
            "http://172.16.0.1",
            "http://172.31.255.254:8787",
            "http://192.168.1.20:8787",
            "http://[::1]:8787",
            "http://[fd12:3456::1]:8787",
        ]
        for relay in accepted {
            XCTAssertNoThrow(try RelayURLValidator.validate(relay), relay)
            XCTAssertNoThrow(try PairingCode.decode(makeCode(relay: relay)), relay)
        }
    }

    func testRejectedRelayURLs() throws {
        let insecure = [
            "http://relay.example.com",
            "http://172.32.0.1:8787",
            "http://8.8.8.8",
            "http://192.169.1.1",
            "ftp://relay.example.com",
            "ws://localhost:8787",
        ]
        for relay in insecure {
            assertRejects(try makeCode(relay: relay), with: .insecureRelayURL)
        }
        let invalid = [
            "relay.example.com",
            "https://",
            "https://user:pass@relay.example.com",
            "https://relay.example.com/?a=1",
            "https://relay.example.com/#frag",
            "https://relay.example.com/" + String(repeating: "a", count: 520),
        ]
        for relay in invalid {
            assertRejects(try makeCode(relay: relay), with: .invalidRelayURL)
        }
    }

    // MARK: - Fields

    func testValidCodeWithoutName() throws {
        let payload = try PairingCode.decode(makeCode())
        XCTAssertNil(payload.name)
        XCTAssertEqual(payload.displayName, "AI Agent")
        XCTAssertEqual(payload.safetyCode, "F696-2D81-0521")
    }

    func testNameIsCleaned() throws {
        let payload = try PairingCode.decode(makeCode(name: "  My\u{0007} Agent \n"))
        XCTAssertEqual(payload.name, "My Agent")
    }

    func testRejectsMissingPrefix() throws {
        let code = try makeCode()
        assertRejects(String(code.dropFirst(4)), with: .missingPrefix)
        assertRejects("HB2:" + code.dropFirst(4), with: .missingPrefix)
    }

    func testRejectsBadEncoding() {
        assertRejects("HB1:not base64!", with: .invalidEncoding)
        assertRejects("HB1:" + B64.encode(Data("{}".utf8)) + "=", with: .invalidEncoding)
    }

    func testRejectsNonJSON() {
        assertRejects("HB1:" + B64URL.encode(Data("hello".utf8)), with: .invalidJSON)
    }

    func testRejectsTooLongCode() {
        assertRejects("HB1:" + String(repeating: "A", count: 5000), with: .tooLong)
    }

    func testRejectsWrongVersion() throws {
        assertRejects(try makeCode(version: 2), with: .unsupportedVersion)
    }

    func testRejectsInvalidAccount() throws {
        assertRejects(try makeCode(account: "hb_tooShort"), with: .invalidAccount)
        assertRejects(try makeCode(account: "ab_oKGio6SlpqeoqaqrrK2urw"), with: .invalidAccount)
    }

    func testRejectsInvalidKeys() throws {
        assertRejects(try makeCode(sig: B64.encode(Data(repeating: 1, count: 31))), with: .invalidSigningKey)
        assertRejects(try makeCode(sig: B64URL.encode(Data(repeating: 0xfb, count: 32))), with: .invalidSigningKey)
        assertRejects(try makeCode(enc: B64.encode(Data(repeating: 1, count: 33))), with: .invalidEncryptionKey)
    }

    func testRejectsInvalidSecret() throws {
        assertRejects(try makeCode(secret: B64URL.encode(Data(repeating: 7, count: 31))), with: .invalidSecret)
        // Padded or standard Base64 is not b64u.
        assertRejects(try makeCode(secret: B64.encode(Data(repeating: 0xfb, count: 32))), with: .invalidSecret)
    }

    func testRejectsLongName() throws {
        XCTAssertNoThrow(try PairingCode.decode(makeCode(name: String(repeating: "ä", count: 64))))
        assertRejects(try makeCode(name: String(repeating: "a", count: 65)), with: .nameTooLong)
    }

    // MARK: - Base64 helpers

    func testBase64URLStrictness() {
        XCTAssertEqual(B64URL.encode(Data([0xfb, 0xff])), "-_8")
        XCTAssertEqual(B64URL.decode("-_8"), Data([0xfb, 0xff]))
        XCTAssertNil(B64URL.decode("-_8="), "padding is not allowed")
        XCTAssertNil(B64URL.decode("+/8"), "standard alphabet is not allowed")
        XCTAssertNil(B64URL.decode("-_9"), "non-canonical trailing bits")
        XCTAssertNil(B64URL.decode("A"), "impossible length")
        XCTAssertNil(B64URL.decode("AAAA", length: 2))
    }

    func testBase64Strictness() {
        XCTAssertEqual(B64.decode("+/8="), Data([0xfb, 0xff]))
        XCTAssertNil(B64.decode("+/8"), "padding is required")
        XCTAssertNil(B64.decode("-_8="), "URL alphabet is not allowed")
    }
}
