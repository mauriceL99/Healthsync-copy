# HealthBridge für iOS

Die iPhone-App von **HealthBridge**. Sie liest die ausgewählten Apple-Health-Daten,
verschlüsselt sie **auf dem Gerät** für den gekoppelten Agent (CLI) und lädt nur
Chiffretext auf den Relay-Server. Der Agent holt die Umschläge ab, entschlüsselt sie
lokal und stellt die Daten Claude Code, Codex, OpenClaw usw. zur Verfügung.

Normative Grundlagen: [`docs/PROTOCOL.md`](../docs/PROTOCOL.md) (Pairing, signierte
Requests, Relay-API, Envelope-Verschlüsselung) und
[`docs/DATA_SCHEMA.md`](../docs/DATA_SCHEMA.md) (Klartext-Format `HealthBatch`).

> HealthBridge ist kein Medizinprodukt. Die App stellt keine Diagnosen und ersetzt
> keine ärztliche Beratung.

## Voraussetzungen

* macOS mit **Xcode 15.3** oder neuer (iOS-17-SDK, Swift 5.10), besser Xcode 16
* [XcodeGen](https://github.com/yonaskolb/XcodeGen)
* Ein Apple-Developer-Account (HealthKit funktioniert nur mit Signierung; für
  Background Delivery und In-App-Käufe ist ein bezahlter Account nötig)
* Ein echtes iPhone für HealthKit-Tests (der Simulator hat keine echten Health-Daten
  und keine Kamera)

## Projekt erzeugen

```bash
brew install xcodegen
cd ios
xcodegen            # erzeugt HealthBridge.xcodeproj aus project.yml
open HealthBridge.xcodeproj
```

Das `.xcodeproj` wird nicht eingecheckt; nach Änderungen an `project.yml` einfach
`xcodegen` erneut ausführen.

Projekteinstellungen (aus `project.yml`):

| Einstellung | Wert |
| --- | --- |
| Targets | `HealthBridge` (App), `HealthBridgeTests` (Unit-Tests) |
| Deployment Target | iOS 17.0, nur iPhone |
| Swift | Swift-5-Sprachmodus, `SWIFT_STRICT_CONCURRENCY = minimal` |
| Bundle-ID | `com.example.healthbridge` (**Platzhalter**) |
| Info.plist | `HealthBridge/Info.plist` (von Hand gepflegt, nicht generiert) |
| Entitlements | `HealthBridge/HealthBridge.entitlements` |
| StoreKit-Konfiguration | `Configuration/Products.storekit` (im Scheme hinterlegt) |

## Signierung und Team

1. In `project.yml` `DEVELOPMENT_TEAM` auf die eigene Team-ID setzen (oder in Xcode
   unter *Signing & Capabilities* das Team wählen).
2. Die Platzhalter-Bundle-ID `com.example.healthbridge` durch eine eigene ersetzen,
   z. B. `de.meinname.healthbridge`:
   * `PRODUCT_BUNDLE_IDENTIFIER` in `project.yml` (App und Tests). Die IDs der
     Hintergrund-Tasks (`<bundle-id>.refresh`/`.backfill`, auch in `Info.plist`) und der
     Produkte (`<bundle-id>.pro.lifetime`/`.pro.yearly`) leitet die App daraus ab
     (`HealthBridge/App/AppIdentifiers.swift`).
   * Dieselben Produkt-IDs in `Configuration/Products.storekit` und in App Store Connect
     anlegen.
   * Keychain-Service in `HealthBridge/Security/KeychainStore.swift` (optional; eine
     Änderung macht vorhandene Keychain-Einträge unerreichbar).
3. Automatische Signierung legt die App-ID mit den Capabilities aus der
   Entitlements-Datei an.

## Capabilities

### HealthKit

`HealthBridge.entitlements` enthält:

* `com.apple.developer.healthkit = true`
* `com.apple.developer.healthkit.access = []` (keine Clinical Records)
* `com.apple.developer.healthkit.background-delivery = true` (für „Bei neuen
  Health-Daten“)

In Xcode erscheint das als *HealthKit* mit aktivierter *Background Delivery*.
Die App fragt **nur Lesezugriff** und nur für die Typen der aktivierten Kategorien an
(`NSHealthShareUsageDescription`). Sie schreibt nie in Apple Health, deshalb gibt es
keine `NSHealthUpdateUsageDescription`.

Health-Daten sind bei gesperrtem Gerät verschlüsselt. Ein Hintergrund-Sync bei
gesperrtem iPhone bricht deshalb mit „Health data is protected while your iPhone is
locked“ ab und läuft beim nächsten Mal erneut.

### Background Modes

`UIBackgroundModes` = `fetch`, `processing`. Registrierte Tasks
(`BGTaskSchedulerPermittedIdentifiers`):

| Identifier | Typ | Zweck |
| --- | --- | --- |
| `<bundle-id>.refresh` | `BGAppRefreshTask` | Modus „Geplant“ (1/3/6/12/24 h) und Sicherheitsnetz im Modus „Bei neuen Daten“ |
| `<bundle-id>.backfill` | `BGProcessingTask` | Fortsetzen eines unterbrochenen Verlaufs-Imports |

iOS entscheidet selbst, wann ein Hintergrund-Task tatsächlich läuft. Der eingestellte
Abstand ist nur ein Mindestabstand. Zum Testen im Debugger (App pausieren, dann in der
LLDB-Konsole):

```text
e -l objc -- (void)[[BGTaskScheduler sharedScheduler] _simulateLaunchForTaskWithIdentifier:@"com.example.healthbridge.refresh"]
```

Für „Bei neuen Health-Daten“ registriert die App beim Start `HKObserverQuery`s und
ruft `enableBackgroundDelivery(for:frequency: .hourly)` auf. Mehrere Benachrichtigungen
in kurzer Folge werden zusammengefasst; frühestens 15 Minuten nach dem letzten
erfolgreichen Sync wird erneut synchronisiert.

### Kamera

`NSCameraUsageDescription` für den QR-Scanner (VisionKit `DataScannerViewController`).
Ohne Kamera kann der Pairing-Code (`HB1:…`) eingefügt werden.

## StoreKit testen

Das Scheme verweist auf `Configuration/Products.storekit`. Damit funktionieren Käufe
im Simulator und auf dem Gerät ohne App Store Connect:

| Produkt-ID | Typ |
| --- | --- |
| `com.example.healthbridge.pro.lifetime` | Non-Consumable |
| `com.example.healthbridge.pro.yearly` | Auto-Renewable (Gruppe „HealthBridge Pro“, 1 Woche gratis) |

*Debug › StoreKit › Manage Transactions…* zeigt die Testkäufe; dort lassen sich Käufe
löschen, erstatten oder Verlängerungen beschleunigen. Für TestFlight und den App
Store müssen dieselben Produkte in App Store Connect angelegt werden.

| | Kostenlos | Pro |
| --- | --- | --- |
| Manueller Sync | ✓ | ✓ |
| Geplanter Sync / Sync bei neuen Daten | – | ✓ |
| Verlauf | 7 Tage | bis 365 Tage |
| Workout-Herzfrequenzdetails (Zonen, Kurve, Erholung) | – | ✓ |

## Gegen ein lokales Relay testen

Der Pairing-Code erlaubt `http://` nur für `localhost`, Loopback- und private
Netzwerkadressen (`10/8`, `172.16/12`, `192.168/16`, `fc00::/7`), z. B.
`http://192.168.1.20:8787`.

1. Relay auf dem Mac starten (Port 8787, an alle Interfaces gebunden).
2. Im Agent als Relay-URL `http://<Mac-LAN-IP>:8787` verwenden und den Pairing-Code
   bzw. QR-Code erzeugen. `localhost` funktioniert nur im Simulator, nicht auf dem
   iPhone.
3. Beim ersten Zugriff fragt iOS nach **lokalem Netzwerkzugriff**
   (`NSLocalNetworkUsageDescription`) – erlauben.

**App Transport Security:** Für Klartext-HTTP im lokalen Netz ist in `Info.plist`
bereits

```xml
<key>NSAppTransportSecurity</key>
<dict>
    <key>NSAllowsLocalNetworking</key>
    <true/>
</dict>
```

gesetzt. Das betrifft nur lokale Adressen; Internet-Relays müssen weiterhin HTTPS
(TLS ≥ 1.2) verwenden. Wer für die Store-Version keine lokalen Relays unterstützen
will, kann den Eintrag und `NSLocalNetworkUsageDescription` entfernen. Redirects
lehnt die App grundsätzlich ab.

Ohne iPhone lässt sich das Relay mit `healthbridge dev simulate-device` testen
(siehe Haupt-README).

## Tests

```bash
xcodebuild test -project HealthBridge.xcodeproj -scheme HealthBridge \
  -destination 'platform=iOS Simulator,name=iPhone 15'
```

* `ProtocolVectorTests` prüft gegen `HealthBridgeTests/Resources/protocol_vectors.json`
  (byte-genaue Kopie von `testdata/protocol_vectors.json`): Pairing-Code, Safety-Code,
  `kid`, Pairing-Hash, String-to-sign, Ed25519-Signatur, ECDH-Shared-Secret,
  HKDF-Schlüssel, AAD, byte-genaues Neuversiegeln des `none`-Vektors und Öffnen beider
  Vektoren (inkl. Raw-DEFLATE). Wird `testdata/protocol_vectors.json` neu erzeugt,
  muss die Kopie aktualisiert werden.
* `PairingCodeTests` prüft die Validierungsregeln (Relay-URL, Längen, Base64-Varianten).
* `HealthBatchEncodingTests` prüft JSON-Schlüssel, Weglassen leerer Felder und den
  Metrik-Katalog gegen `docs/DATA_SCHEMA.md`.
* `HealthLogicTests` prüft Schlaf-Zuordnung (18-Uhr-Fenster, überlappende Quellen),
  Herzfrequenz-Zonen/-Kurve/-Erholung und die Sync-Planung.

Hinweis: CryptoKit erzeugt Ed25519-Signaturen randomisiert. Die Tests vergleichen
daher nicht die Signatur-Bytes, sondern verifizieren sie.

## Aufbau

```text
HealthBridge/
  App/        Einstieg (BGTask-Registrierung, Observer beim Start), AppModel
  Protocol/   Base64, Pairing-Code, Request-Signatur, Envelope-Krypto, RelayClient
  Security/   Keychain (Geräteschlüssel, Pairing-Datensatz)
  Health/     Metrik-Katalog, HealthKit-Zugriff, Schlaf- und Herzfrequenzlogik
  Sync/       HealthBatch-Modelle, Builder, SyncEngine, Hintergrund-Sync, Aktivitätsprotokoll
  Store/      StoreKit 2 (HealthBridge Pro)
  Views/      SwiftUI: Onboarding, Home, Metriken, Protokoll, Einstellungen, Paywall
```

Ablauf eines Syncs: Plan (Backfill des gewählten Zeitraums beim ersten Mal, danach die
letzten 3 Tage bzw. die Lücke seit dem letzten Sync) → je 30 Tage ein `HealthBatch`
→ JSON → Raw DEFLATE → X25519/HKDF-SHA256/ChaCha20-Poly1305 → bis zu 32 Envelopes pro
`POST /v1/envelopes`. Das Aktivitätsprotokoll enthält nur Metadaten (Zeit, Anzahl,
Größe, Fehler), nie Gesundheitswerte.

Schlüssel: Der Ed25519-Geräteschlüssel und der Pairing-Datensatz liegen in der
Keychain (`kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly`, damit Hintergrund-Syncs
funktionieren und nichts per Backup auf andere Geräte wandert). Das Pairing-Secret wird
nach dem Koppeln nicht gespeichert. Nach einer Neuinstallation wird der alte
Pairing-Datensatz verworfen, der Geräteschlüssel bleibt erhalten – erneutes Koppeln mit
demselben QR-Code ist dadurch auf dem Relay idempotent. „Entkoppeln“ ruft
`DELETE /v1/devices/self` auf und löscht alle Keychain-Einträge. Meldet das Relay
`410 device_unlinked`, kehrt die App in den ungekoppelten Zustand zurück. `401
unknown_device` (z. B. nach einem Relay-Reset) entkoppelt dagegen nicht still: Der Sync
schlägt mit einem Hinweis fehl, und der Nutzer entscheidet, ob er neu koppelt.

## Hinweise für das App-Review

* **HealthKit-Nutzung:** Die App liest Health-Daten ausschließlich, um sie auf
  ausdrücklichen Wunsch des Nutzers verschlüsselt an dessen eigenen Agent zu senden.
  Keine Werbung, kein Verkauf, keine Analyse-SDKs, keine Speicherung von Health-Daten
  in iCloud. Das muss so auch in der Datenschutzerklärung und den App-Privacy-Angaben
  („Health & Fitness“, nicht zum Tracking verwendet) stehen.
* **Datenschutzerklärung:** Für HealthKit-Apps zwingend (URL in App Store Connect und
  idealerweise in der App). Sie muss das Relay (Betreiber, Speicherdauer max. 30 Tage
  bzw. bis zum Abholen, nur Chiffretext) beschreiben.
* **Review-Zugang:** Das Review-Team braucht einen funktionierenden Agent/Relay. Am
  einfachsten einen Pairing-Code eines Test-Agents in den Review-Notes hinterlegen
  (Code einfügen statt scannen) und erklären, dass es sich um ein
  Entwickler-/Automatisierungswerkzeug handelt.
* **Kein Medizinprodukt:** Der Hinweis steht im Onboarding und in den Einstellungen.
  Die Store-Beschreibung sollte keine medizinischen Versprechen machen.
* **Export Compliance:** Die App verwendet Verschlüsselung (CryptoKit:
  X25519, HKDF, ChaCha20-Poly1305, Ed25519) über HTTPS hinaus. `ITSAppUsesNonExemptEncryption`
  ist deshalb bewusst **nicht** in `Info.plist` gesetzt – App Store Connect fragt beim
  Upload danach. Die Verschlüsselung dient dem Schutz von Nutzerdaten und nutzt nur
  Standardalgorithmen der Betriebssystem-Frameworks; je nach Einordnung fällt das unter
  die Ausnahmen der EAR (Kategorie 5 Teil 2), ggf. ist eine Selbstklassifizierung
  (Annual Self-Classification Report) einzureichen. Das bitte vor der Veröffentlichung
  rechtlich prüfen und dann die Frage in App Store Connect bzw. den Info.plist-Schlüssel
  passend setzen.
* **App-Icon:** `Assets.xcassets/AppIcon.appiconset` enthält nur die Slots
  (1024×1024, hell/dunkel/getönt). Vor dem Upload ein Icon ergänzen.
* **Name:** Die App heißt „HealthBridge“ und verwendet weder Namen noch Branding des
  Originals.
