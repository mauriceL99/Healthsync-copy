# Privacy Policy – HealthBridge (draft)

> **Draft, not legal advice.** Replace the `[placeholders]` and have it reviewed before
> external TestFlight or App Store release. It describes the code in this repository;
> keep it in sync when the code changes. German version: `datenschutz.md`.

Last updated: [date]

## Summary

* HealthBridge reads the Apple Health data you select, **encrypts it on your iPhone** and
  sends it to the AI agent you paired the app with.
* The publisher of the app receives **none** of your data. No ads, no tracking, no
  analytics or advertising SDKs.
* Data travels through a relay server chosen by you (your agent). The relay only sees
  encrypted data and a few metadata fields (see "Relay").

## Controller

[First name Last name], [address], [email]. Whoever operates the relay is responsible
for it; by default with HealthBridge that is you.

## On your iPhone

| Data | Purpose | Stored where |
| --- | --- | --- |
| Apple Health data of the categories you choose | sending it to your agent | read only, not kept by the app |
| Pairing data (agent public keys, relay address, device ID) and the device private key | encryption and relay sign-in | iOS keychain, bound to this device |
| Settings and sync status | app function | app settings on the device |
| Activity log (times, number and size of uploads, error messages; **no health values**) | shown in the app | app file, protected until first unlock |

The camera is used only to scan the pairing QR code; no images are stored. Health data is
not stored in iCloud, not used for advertising and not sold or shared with third parties.

## Relay

Each upload is encrypted on the iPhone for your agent's public key (X25519, HKDF-SHA256,
ChaCha20-Poly1305); only your agent can decrypt it. The relay stores:

| Data | Retention |
| --- | --- |
| encrypted packages (unreadable for the relay), their size and time | until your agent fetches them, at most 30 days |
| device record: random device ID, device public key, device name (as set in iOS), platform, app version, link and last-seen time | until the agent deletes its relay account (`healthbridge unlink --delete-relay-account`) |
| IP address | briefly in memory for rate limiting, not logged |

After decryption the data lives with your agent, on the computer where you set up the
HealthBridge CLI. What the AI agent (e.g. Claude Code) does with it is governed by its
provider and your settings there.

[If the publisher operates a relay for users: hosting provider and location, data
processing agreement, retention as above, contact for deletion requests.]

## Legal basis (GDPR)

Health data: your explicit consent (Art. 9(2)(a), Art. 6(1)(a) GDPR), given through the
iOS Health permission and by pairing with your agent. Withdraw it any time in iOS
Settings → Health → Data Access & Devices → HealthBridge, or with **Unpair this iPhone**
in the app.

## Purchases (store edition only)

If the app offers a paid "Pro" option, Apple processes the purchase; the publisher
receives no payment data. The app only checks on the device whether a valid purchase
exists.

## Deleting your data

* **App:** "Unpair this iPhone" unlinks the device at the relay and deletes all keys and
  pairing data; deleting the app removes settings and the activity log.
* **Relay:** `healthbridge unlink --delete-relay-account` deletes account, device and
  unfetched packages.
* **Agent:** decrypted data is stored in `~/.healthbridge` on the agent's computer.

## Your rights

You have the rights of access, rectification, erasure, restriction, data portability and
objection, and the right to lodge a complaint with a supervisory authority. Because the
publisher receives none of your data, these rights usually apply to the relay operator.
Questions: [email].

## Not a medical device

HealthBridge is not a medical device, makes no diagnoses and does not replace medical
advice.
