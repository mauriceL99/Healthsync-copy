import CryptoKit
import Foundation
import Security

// MARK: - Wire types

struct RegisterDeviceRequest: Encodable {
    let account: String
    let secret: String
    let deviceSig: String
    let deviceName: String
    let platform: String
    let appVersion: String

    enum CodingKeys: String, CodingKey {
        case account, secret
        case deviceSig = "device_sig"
        case deviceName = "device_name"
        case platform
        case appVersion = "app_version"
    }
}

struct RegisterDeviceResponse: Decodable, Sendable {
    let deviceID: String
    let account: String
    let linkedAt: String?

    enum CodingKeys: String, CodingKey {
        case deviceID = "device_id"
        case account
        case linkedAt = "linked_at"
    }
}

struct DeviceStatusResponse: Decodable, Equatable, Sendable {
    let deviceID: String
    let account: String
    let linked: Bool
    let pendingEnvelopes: Int?
    let agentName: String?

    enum CodingKeys: String, CodingKey {
        case deviceID = "device_id"
        case account
        case linked
        case pendingEnvelopes = "pending_envelopes"
        case agentName = "agent_name"
    }
}

struct UploadEnvelopesRequest: Encodable {
    let envelopes: [Envelope]
}

struct UploadEnvelopesResponse: Decodable, Sendable {
    struct Accepted: Decodable, Sendable {
        let id: String
        let seq: Int64
    }

    let accepted: [Accepted]
}

private struct OKResponse: Decodable {
    let ok: Bool?
}

private struct ErrorResponse: Decodable {
    struct Body: Decodable {
        let code: String?
        let message: String?
    }

    let error: Body
}

// MARK: - Errors

enum RelayError: LocalizedError, Equatable {
    case invalidRelayURL
    case offline
    case timedOut
    case insecureConnection
    case network(String)
    case redirectRejected
    case invalidResponse
    case deviceUnlinked
    case deviceAlreadyLinked
    case pairingRejected(String?)
    case clockSkew
    case payloadTooLarge
    case rateLimited
    case server(status: Int, code: String?, message: String?)

    var errorDescription: String? {
        switch self {
        case .invalidRelayURL:
            return "The relay address is invalid."
        case .offline:
            return "No internet connection."
        case .timedOut:
            return "The relay did not respond in time."
        case .insecureConnection:
            return "The connection to the relay is not secure. Use https, or allow local networking for development relays."
        case .network(let message):
            return "Network error: \(message)"
        case .redirectRejected:
            return "The relay tried to redirect the request. Redirects are rejected for security."
        case .invalidResponse:
            return "The relay sent an unexpected response."
        case .deviceUnlinked:
            return "This iPhone was unlinked by your agent. Pair again to continue syncing."
        case .deviceAlreadyLinked:
            return "Another iPhone is already linked to this agent. Unlink it on the agent first, then try again."
        case .pairingRejected(let message):
            if let message {
                return "The relay rejected the pairing code: \(message)"
            }
            return "The relay rejected the pairing code. Create a new code on your agent."
        case .clockSkew:
            return "The relay rejected the request time. Make sure your iPhone sets date & time automatically."
        case .payloadTooLarge:
            return "The upload was too large for the relay."
        case .rateLimited:
            return "The relay is limiting requests right now. Try again later."
        case .server(let status, let code, let message):
            var text = "The relay reported an error (HTTP \(status)"
            if let code { text += ", \(code)" }
            text += ")"
            if let message { text += ": \(message)" }
            return text
        }
    }
}

// MARK: - Client

/// Talks to the HB1 relay. Every request is signed with the device's Ed25519 key.
struct RelayClient: Sendable {
    /// Base URL without trailing slash, e.g. `https://relay.example.com` or `https://host/prefix`.
    let baseURL: String
    /// Percent-encoded base path (`""` or `/prefix`), prepended to every signed request path.
    let basePath: String

    init(relay: String) throws {
        do {
            _ = try RelayURLValidator.validate(relay)
        } catch {
            throw RelayError.invalidRelayURL
        }
        var trimmed = relay
        while trimmed.hasSuffix("/") {
            trimmed.removeLast()
        }
        guard let components = URLComponents(string: trimmed) else {
            throw RelayError.invalidRelayURL
        }
        baseURL = trimmed
        basePath = components.percentEncodedPath
    }

    // MARK: Endpoints

    /// `POST /v1/devices` with `HB-Key: new-device`, signed by the new device key.
    func registerDevice(
        pairing: PairingPayload,
        signingKey: Curve25519.Signing.PrivateKey,
        deviceName: String,
        appVersion: String
    ) async throws -> RegisterDeviceResponse {
        let body = RegisterDeviceRequest(
            account: pairing.account,
            secret: pairing.secret,
            deviceSig: B64.encode(signingKey.publicKey.rawRepresentation),
            deviceName: deviceName,
            platform: "ios",
            appVersion: appVersion
        )
        do {
            return try await send(
                method: "POST",
                path: "/v1/devices",
                key: RelayKey.newDevice,
                body: try Self.encoder.encode(body),
                signingKey: signingKey,
                as: RegisterDeviceResponse.self
            )
        } catch RelayError.server(let status, _, let message) where status == 401 || status == 403 || status == 404 {
            throw RelayError.pairingRejected(message)
        }
    }

    /// `GET /v1/devices/self`
    func deviceStatus(deviceID: String, signingKey: Curve25519.Signing.PrivateKey) async throws -> DeviceStatusResponse {
        try await send(
            method: "GET",
            path: "/v1/devices/self",
            key: RelayKey.device(deviceID),
            body: nil,
            signingKey: signingKey,
            as: DeviceStatusResponse.self
        )
    }

    /// `DELETE /v1/devices/self`
    func unlinkDevice(deviceID: String, signingKey: Curve25519.Signing.PrivateKey) async throws {
        _ = try await send(
            method: "DELETE",
            path: "/v1/devices/self",
            key: RelayKey.device(deviceID),
            body: nil,
            signingKey: signingKey,
            as: OKResponse.self
        )
    }

    /// `POST /v1/envelopes` (at most 32 envelopes per request).
    func upload(
        envelopes: [Envelope],
        deviceID: String,
        signingKey: Curve25519.Signing.PrivateKey
    ) async throws -> UploadEnvelopesResponse {
        guard envelopes.count <= HB1.maxEnvelopesPerRequest else {
            throw RelayError.payloadTooLarge
        }
        return try await send(
            method: "POST",
            path: "/v1/envelopes",
            key: RelayKey.device(deviceID),
            body: try Self.encoder.encode(UploadEnvelopesRequest(envelopes: envelopes)),
            signingKey: signingKey,
            as: UploadEnvelopesResponse.self
        )
    }

    // MARK: Transport

    static let encoder: JSONEncoder = {
        let encoder = JSONEncoder()
        encoder.outputFormatting = [.withoutEscapingSlashes]
        return encoder
    }()

    private static let session: URLSession = {
        let configuration = URLSessionConfiguration.ephemeral
        configuration.timeoutIntervalForRequest = 30
        configuration.timeoutIntervalForResource = 120
        configuration.waitsForConnectivity = false
        configuration.httpShouldSetCookies = false
        configuration.httpCookieAcceptPolicy = .never
        configuration.urlCache = nil
        configuration.requestCachePolicy = .reloadIgnoringLocalCacheData
        configuration.tlsMinimumSupportedProtocolVersion = .TLSv12
        configuration.httpAdditionalHeaders = ["User-Agent": "HealthBridge-iOS/\(AppInfo.version)"]
        return URLSession(configuration: configuration, delegate: RedirectBlocker(), delegateQueue: nil)
    }()

    private func send<Response: Decodable>(
        method: String,
        path: String,
        key: String,
        body: Data?,
        signingKey: Curve25519.Signing.PrivateKey,
        as type: Response.Type
    ) async throws -> Response {
        let pathAndQuery = basePath + path
        guard let url = URL(string: baseURL + path) else {
            throw RelayError.invalidRelayURL
        }
        var request = URLRequest(url: url)
        request.httpMethod = method
        request.setValue("application/json", forHTTPHeaderField: "Accept")
        let bodyData = body ?? Data()
        if let body {
            request.httpBody = body
            request.setValue("application/json", forHTTPHeaderField: "Content-Type")
        }
        let headers = try RequestSigner.sign(
            privateKey: signingKey,
            method: method,
            pathAndQuery: pathAndQuery,
            key: key,
            body: bodyData
        )
        headers.apply(to: &request)

        let result: (Data, URLResponse)
        do {
            result = try await Self.session.data(for: request)
        } catch let error as URLError {
            throw Self.map(error)
        }
        let (data, response) = result
        guard let http = response as? HTTPURLResponse else {
            throw RelayError.invalidResponse
        }
        switch http.statusCode {
        case 200..<300:
            do {
                return try JSONDecoder().decode(Response.self, from: data)
            } catch {
                throw RelayError.invalidResponse
            }
        case 300..<400:
            throw RelayError.redirectRejected
        default:
            throw Self.mapError(status: http.statusCode, data: data)
        }
    }

    private static func map(_ error: URLError) -> Error {
        switch error.code {
        case .cancelled:
            return CancellationError()
        case .notConnectedToInternet, .networkConnectionLost, .dataNotAllowed, .internationalRoamingOff:
            return RelayError.offline
        case .timedOut:
            return RelayError.timedOut
        case .appTransportSecurityRequiresSecureConnection, .secureConnectionFailed,
             .serverCertificateUntrusted, .serverCertificateHasBadDate, .serverCertificateNotYetValid,
             .serverCertificateHasUnknownRoot, .clientCertificateRejected, .clientCertificateRequired:
            return RelayError.insecureConnection
        case .httpTooManyRedirects, .redirectToNonExistentLocation:
            return RelayError.redirectRejected
        default:
            return RelayError.network(error.localizedDescription)
        }
    }

    private static func mapError(status: Int, data: Data) -> RelayError {
        let body = try? JSONDecoder().decode(ErrorResponse.self, from: data)
        let code = body?.error.code.map { String($0.prefix(64)) }
        let message = body?.error.message.map { HB1.cleanLabel($0, maxLength: 200) }

        if status == 410 || code == "device_unlinked" {
            return .deviceUnlinked
        }
        switch code {
        case "device_already_linked":
            return .deviceAlreadyLinked
        case "invalid_secret", "invalid_pairing", "pairing_failed", "unknown_account", "account_not_found":
            return .pairingRejected(message)
        case "stale_request", "invalid_timestamp", "timestamp_out_of_range", "clock_skew":
            return .clockSkew
        default:
            break
        }
        switch status {
        case 413:
            return .payloadTooLarge
        case 429:
            return .rateLimited
        default:
            return .server(status: status, code: code, message: message)
        }
    }
}

/// Rejects every HTTP redirect so signed requests never leave the paired relay.
private final class RedirectBlocker: NSObject, URLSessionTaskDelegate, @unchecked Sendable {
    func urlSession(
        _ session: URLSession,
        task: URLSessionTask,
        willPerformHTTPRedirection response: HTTPURLResponse,
        newRequest request: URLRequest,
        completionHandler: @escaping (URLRequest?) -> Void
    ) {
        completionHandler(nil)
    }
}
