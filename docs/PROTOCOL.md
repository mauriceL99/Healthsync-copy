# HealthBridge Protocol v1 (HB1)

This document is normative for all three components:

* **iOS app** (`ios/`), which reads HealthKit, encrypts and uploads.
* **Relay** (`cmd/healthbridge-relay`), which stores ciphertext only.
* **Agent CLI** (`cmd/healthbridge`), which fetches, decrypts, stores and summarizes.

Test vectors live in [`testdata/protocol_vectors.json`](../testdata/protocol_vectors.json)
and are checked by the Go tests and the iOS unit tests.

## 1. Conventions

| Name | Meaning |
| --- | --- |
| `b64` | Standard Base64 **with** padding (RFC 4648 §4) |
| `b64u` | Base64url **without** padding (RFC 4648 §5) |
| `hex` | Lowercase hexadecimal |
| `SHA256` | SHA-256 |
| `‖` | Byte concatenation |
| Timestamps in JSON | RFC 3339, UTC, suffix `Z`, second precision (`2026-10-13T08:00:00Z`) |

All JSON is UTF-8. Receivers ignore unknown JSON fields unless stated otherwise.

## 2. Identities

### Agent (CLI)

* `sig`: Ed25519 key pair, used to sign relay requests.
* `enc`: X25519 key pair, the recipient key for health data.
* `account`: `"hb_" + b64u(16 random bytes)`, which gives 25 characters and matches
  `^hb_[A-Za-z0-9_-]{22}$`.
* `secret`: 32 random bytes, the pairing secret. The relay stores only `SHA256(secret)`.

### Device (iPhone)

* `device_sig`: Ed25519 key pair, generated on the device and kept in the Keychain.
* `device_id`: assigned by the relay, `"dev_" + b64u(16 random bytes)`.

### Key ID

`kid = b64u(SHA256(enc_pub)[0..12])`, which gives 16 characters.

### Safety code

The safety code lets the user compare both screens during pairing.

```text
digest = SHA256("HB1-SAFETY" ‖ 0x00 ‖ UTF8(account) ‖ 0x00 ‖ sig_pub(32) ‖ enc_pub(32))
code   = upper(hex(digest[0..6])) formatted as XXXX-XXXX-XXXX
```

## 3. Pairing code (QR content)

```text
HB1:<b64u(UTF-8 JSON)>
```

JSON object:

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `v` | int | yes | always `1` |
| `relay` | string | yes | Relay base URL, `https://…` (or `http://localhost…`/`http://127.0.0.1…`/private LAN for development) |
| `account` | string | yes | Account ID |
| `sig` | b64 | yes | Agent Ed25519 public key (32 bytes) |
| `enc` | b64 | yes | Agent X25519 public key (32 bytes) |
| `secret` | b64u | yes | Pairing secret (32 bytes) |
| `name` | string | no | Agent label shown in the app, ≤ 64 chars |

The QR code encodes the complete string, including the `HB1:` prefix, in byte mode
with error correction level M.

## 4. Signed requests

Every request except `GET /healthz` carries these headers:

| Header | Value |
| --- | --- |
| `HB-Key` | `agent:<account>`, `device:<device_id>` or `new-device` |
| `HB-Timestamp` | Unix time in seconds, decimal |
| `HB-Nonce` | `b64u(16 random bytes)` |
| `HB-Signature` | `b64(Ed25519 signature)` over the string to sign |

String to sign (UTF-8, joined with `\n`, no trailing newline):

```text
HB1-REQUEST
<HTTP method, uppercase>
<request path including query string, exactly as sent, e.g. /v1/envelopes?after=0&limit=50>
<HB-Key value>
<HB-Timestamp value>
<HB-Nonce value>
<hex(SHA256(raw request body))>   (an empty body hashes the empty string)
```

The relay verifies each request as follows:

* `|now − timestamp| ≤ 300 s`.
* Each nonce may be used only once per key within 600 s.
* For `agent:<account>`, the key is the stored agent `sig`.
* For `device:<device_id>`, the key is the stored `device_sig`. An unlinked device gets
  `410 device_unlinked`.
* For `new-device`, the key is `device_sig` from the JSON body. This is allowed only
  on `POST /v1/devices`.
* `POST /v1/accounts` is signed with `agent:<account>`, and the key is `sig` from the
  body (self-certifying).

## 5. Relay API

Error responses carry `{"error": {"code": "<snake_case>", "message": "<text>"}}`.

| Method & path | Key | Body → Response |
| --- | --- | --- |
| `GET /healthz` | – | → `{"ok": true, "version": "…"}` |
| `POST /v1/accounts` | agent | `{"account","sig","enc","pairing_hash","name"}` → `{"account","created"}` |
| `GET /v1/status` | agent | → `{"account","device": Device \| null,"pending_envelopes","oldest_pending_at"}` |
| `DELETE /v1/devices/current` | agent | → `{"ok": true, "unlinked": bool}` |
| `GET /v1/envelopes?after=<seq>&limit=<n>` | agent | → `{"envelopes": [StoredEnvelope], "more": bool}` |
| `POST /v1/envelopes/ack` | agent | `{"up_to": <seq>}` → `{"deleted": n}` |
| `DELETE /v1/accounts/self` | agent | → `{"ok": true}` (removes account, devices and envelopes) |
| `POST /v1/devices` | new-device | `{"account","secret","device_sig","device_name","platform","app_version"}` → `201 {"device_id","account","linked_at"}` |
| `GET /v1/devices/self` | device | → `{"device_id","account","linked": true,"pending_envelopes","agent_name"}` |
| `DELETE /v1/devices/self` | device | → `{"ok": true}` |
| `POST /v1/envelopes` | device | `{"envelopes": [Envelope]}` → `{"accepted": [{"id","seq"}]}` |

Rules:

* `pairing_hash` is `b64(SHA256(secret))`. A repeated `POST /v1/accounts` with the same
  `sig` and `enc` updates `pairing_hash` and `name`. A different key returns
  `409 account_exists`.
* A relay may require a registration token for **new** accounts. The client sends it
  in the `HB-Registration-Token` header. Without it the relay returns
  `403 registration_token_required`.
* `POST /v1/devices` checks `SHA256(secret)` against `pairing_hash` (constant time).
  If another device is linked, it returns `409 device_already_linked`. Repeating the
  call with the same `device_sig` is idempotent.
* An account has at most one linked device. Unlinking keeps the pairing secret valid,
  so the same QR code can pair again.
* `POST /v1/envelopes` accepts at most 32 envelopes and a 25 MiB body. Each envelope's
  `account` must equal the device's account. A repeated `id` returns the existing `seq`.
* `GET /v1/envelopes` uses a default `limit` of 50 and a maximum of 200. It returns
  envelopes in ascending `seq` order and stops early after about 32 MiB of ciphertext
  (`more = true`).
* The relay deletes envelopes after an ack or after its TTL (default 30 days).

`Device` = `{"device_id","name","platform","app_version","linked_at","last_seen_at"}`.

### Error codes

| HTTP | `code` | Meaning |
| --- | --- | --- |
| 400 | `invalid_request`, `invalid_envelope`, `unexpected_body` | Malformed request |
| 401 | `unauthenticated` | Missing or malformed `HB-*` headers |
| 401 | `bad_signature` | Signature does not verify |
| 401 | `stale_request` | Timestamp outside ±300 s (check the device clock) |
| 401 | `replayed_request` | Nonce already used |
| 401 | `unknown_account`, `unknown_device` | Key ID is not registered (device: pair again) |
| 403 | `invalid_pairing_secret` | Pairing secret does not match |
| 403 | `wrong_account` | Envelope account differs from the device's account |
| 403 | `registration_token_required` | Relay requires `HB-Registration-Token` for new accounts |
| 404 | `unknown_account` | `POST /v1/devices` for an account that is not registered |
| 409 | `account_exists`, `device_already_linked` | Conflict |
| 410 | `device_unlinked` | Device was unlinked; the app returns to the unpaired state |
| 413 | `body_too_large` | Body exceeds the endpoint limit |
| 429 | `rate_limited` | Too many requests |
| 500 | `internal_error` | Server error |

`StoredEnvelope` = `Envelope` plus `{"seq": int, "received_at": timestamp}`.

## 6. Envelope

```json
{
  "id": "1b4e28ba-2fa1-11d2-883f-0016d3cca427",
  "account": "hb_…",
  "kind": "sync",
  "alg": "HB1-X25519-HKDF-SHA256-CHACHA20POLY1305",
  "kid": "…",
  "epk": "<b64, 32 bytes>",
  "nonce": "<b64, 12 bytes>",
  "compression": "deflate",
  "created_at": "2026-10-13T08:00:00Z",
  "ct": "<b64, ciphertext ‖ 16-byte Poly1305 tag>"
}
```

* `id` is a lowercase UUID string generated by the device.
* `kind` is `sync` (incremental) or `backfill` (history import).
* `compression` is `deflate` (raw DEFLATE, RFC 1951, as produced by Apple's
  `COMPRESSION_ZLIB`) or `none`.

### Encryption (device)

```text
(e_priv, e_pub) = X25519 key pair (fresh per envelope)
shared  = X25519(e_priv, enc_pub)
key     = HKDF-SHA256(ikm = shared, salt = e_pub ‖ enc_pub, info = "HB1-ENVELOPE-KEY", L = 32)
aad     = UTF8("HB1-ENVELOPE\n" + account + "\n" + id + "\n" + kind + "\n" + compression + "\n" + created_at)
nonce   = 12 random bytes
body    = compression == "deflate" ? rawDeflate(plaintext) : plaintext
ct      = ChaCha20-Poly1305-Seal(key, nonce, body, aad)    // ciphertext ‖ tag
```

The receiver rejects envelopes when any of the following holds:

* `alg` or `kid` differs from its own key.
* `account` differs from its own account.
* Authentication fails.
* The decompressed size exceeds 64 MiB.
* The plaintext is not a valid HealthBatch (see [DATA_SCHEMA.md](DATA_SCHEMA.md)).

## 7. Processing order

The agent fetches envelopes in ascending `seq`. It applies them in that order, so a
later upload overrides earlier values per day and per metric. Workouts are merged by
`id`. After the data is stored, the agent acks the highest processed `seq` unless the
user chose `--keep-on-relay`.

An envelope the agent cannot open or validate (for example, one written by a newer
app) is acked like the others, so it cannot block the mailbox. The agent keeps its
ciphertext in a local quarantine, keyed by envelope `id` (sequence numbers are not
stable across relay resets), and retries it on every fetch.

If `POST /v1/accounts` reports `created: true` for an agent that already has a fetch
position, the relay has lost the account (for example, after a database reset). Its
sequence numbers start again at 1, so the agent resets its position to 0.
