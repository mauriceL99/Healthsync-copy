# HealthBridge auf dem Mac starten (Eigennutzung)

Diese Anleitung bringt dich vom frischen Mac zur laufenden App auf deinem iPhone und zu
Health-Daten in Claude Code. Sie ist für die **persönliche Edition** gedacht: Alle
Funktionen sind freigeschaltet, es gibt kein Abo und keinen App-Store-Kontakt.

Zeitbedarf: etwa 1 Stunde, davon der größte Teil Xcode-Download.

**Welcher Weg?** Die App kommt immer über den Mac aufs iPhone (Schritte 1–3). Danach
gibt es zwei Wege:

| | Agent auf dem Mac (Schritte 4–5 hier) | Agent auf eigenem Server ([`SERVER_START.md`](SERVER_START.md)) |
| --- | --- | --- |
| Läuft ohne eingeschalteten Mac | nein | **ja** |
| ChatGPT | nein | **ja** |
| Claude (Browser, Apps) | nein | **ja** |
| Claude Code am Mac | ja | ja |
| Kosten | keine | kleiner Mietserver, ca. 4–7 €/Monat |
| Wo die entschlüsselten Daten liegen | nur auf deinem Mac | auf deinem gemieteten Server |

Wenn du ChatGPT nutzen willst oder der Mac nicht ständig laufen soll: nach Schritt 3
mit [`SERVER_START.md`](SERVER_START.md) weitermachen.

## Vorab: kostenlose oder bezahlte Apple-ID?

| | Kostenlose Apple-ID („Personal Team“) | Apple Developer Program (99 €/Jahr) |
| --- | --- | --- |
| App auf dem eigenen iPhone | ja | ja |
| Wie lange läuft die App? | **7 Tage**, dann per Xcode neu installieren | 1 Jahr |
| Freunde | nur, wenn du ihr iPhone an deinen Mac anschließt (ebenfalls 7 Tage) | per **TestFlight**-Link |
| Hintergrund-Sync (HealthKit Background Delivery) | wahrscheinlich ja, nicht geprüft¹ | ja |

¹ Welche Capabilities ein Personal Team signieren darf, legt Apple fest und ändert es
gelegentlich. Wenn Xcode beim Signieren meldet, dass das Personal Team eine Capability
nicht unterstützt, schick mir die genaue Meldung. Dann passe ich die Entitlements für
diesen Fall an.

Für den Anfang reicht die kostenlose Apple-ID. Upgraden kannst du jederzeit.

## 1. Werkzeuge installieren

1. **Xcode** aus dem Mac App Store installieren (mehrere GB), einmal starten und die
   iOS-Komponenten installieren lassen.
2. **Homebrew** (falls noch nicht vorhanden), im Terminal:
   ```bash
   /bin/bash -c "$(curl -fsSL https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh)"
   ```
   Am Ende zeigt der Installer zwei Zeilen zum Ergänzen von `PATH` an. Diese ausführen.
3. **Go und XcodeGen:**
   ```bash
   brew install go xcodegen
   ```
4. **Repository holen:**
   ```bash
   git clone https://github.com/mauriceL99/Healthsync-copy.git
   cd Healthsync-copy
   ```

## 2. Apple-ID in Xcode anmelden

Xcode → **Settings… → Accounts → +** → Apple ID. Danach das Team (bei kostenloser
Apple-ID „Dein Name (Personal Team)“) auswählen → **Manage Certificates… → + → Apple
Development**. Aus diesem Zertifikat liest das Setup-Skript deine Team-ID.

## 3. App bauen und aufs iPhone bringen

```bash
cd ios
./setup.sh
```

Das Skript prüft Xcode und XcodeGen und fragt nach der **Team-ID** (wird automatisch
erkannt, Enter genügt). Danach fragt es nach dem **Bundle-ID-Präfix** (Vorschlag
`de.<benutzername>`, z. B. `de.maurice`). Daraus wird die App-ID
`de.maurice.healthbridge`. Beides landet in `ios/Configuration/Local.xcconfig`, die
nicht eingecheckt wird. Anschließend öffnet sich Xcode.

1. iPhone per Kabel verbinden, entsperren und „Diesem Computer vertrauen“ bestätigen.
2. In Xcode oben in der Leiste das iPhone als Ziel wählen und **⌘R** drücken.
3. Nur beim ersten Mal:
   * **Entwicklermodus:** iPhone → Einstellungen → Datenschutz & Sicherheit →
     Entwicklermodus → ein (das iPhone startet neu).
   * Nur bei kostenloser Apple-ID: iPhone → Einstellungen → Allgemein → VPN und
     Geräteverwaltung → dein Entwicklerzertifikat → **Vertrauen**.
   * Dann in Xcode erneut ⌘R.

Typische Meldungen:

| Meldung in Xcode | Lösung |
| --- | --- |
| „Failed to register bundle identifier“ / „not available“ | Das Präfix ist vergeben. `./setup.sh` erneut ausführen, mit **n** antworten und ein anderes Präfix wählen |
| „No Account for Team …“ | Schritt 2 (Apple-ID in Xcode) fehlt |
| „Personal development teams … do not support …“ | Siehe Fußnote oben. Meldung an mich |
| „Could not launch … invalid code signature“ | Zertifikat auf dem iPhone vertrauen (Schritt 3) |

Bei kostenloser Apple-ID läuft die App nach 7 Tagen ab. Dann iPhone anschließen und in
Xcode erneut ⌘R drücken. Daten und Kopplung bleiben erhalten.

## 4. Agent (CLI) und Relay einrichten

Im Repository-Ordner:

```bash
make install PREFIX="$(brew --prefix)"   # healthbridge und healthbridge-relay landen im PATH
healthbridge self-test
```

### Variante A: Relay auf dem Mac (zum Start, zu Hause)

Terminal-Tab 1, das Relay muss laufen bleiben:

```bash
healthbridge-relay --db ~/.healthbridge-relay.db
```

macOS fragt eventuell, ob eingehende Verbindungen erlaubt werden. Zulassen.

Terminal-Tab 2:

```bash
healthbridge onboard --relay "http://$(ipconfig getifaddr en0):8787" --name "MacBook"
```

Ist `ipconfig getifaddr en0` leer (z. B. bei LAN-Kabel), die IP unter
Systemeinstellungen → Netzwerk nachsehen und direkt einsetzen.

In der App: **Get started** → QR-Code scannen → **Sicherheitscode vergleichen**
(muss mit dem Terminal übereinstimmen) → Daten auswählen → Health-Zugriff erlauben →
**Sync now**. Beim ersten Kontakt fragt iOS nach Zugriff auf das **lokale Netzwerk**.
Erlauben.

```bash
healthbridge fetch
healthbridge summary --period week
healthbridge dashboard --days 30     # → ~/.healthbridge/dashboard.html
```

Einschränkung: Hintergrund-Syncs kommen nur an, wenn das iPhone im selben WLAN ist und
das Relay auf dem wachen Mac läuft. Fehlgeschlagene Syncs holt die App beim nächsten Mal
nach.

### Variante B: Sync auch unterwegs

Das Relay braucht dafür eine HTTPS-Adresse, die das iPhone von überall erreicht:

* **Tailscale** (ohne eigenen Server): Tailscale auf Mac und iPhone installieren und im
  Tailscale-Admin MagicDNS und HTTPS-Zertifikate einschalten. Dann das Relay wie in
  Variante A starten und mit `tailscale serve` per HTTPS freigeben (siehe die
  Tailscale-Doku zu `tailscale serve`). Die Adresse hat die Form
  `https://<mac-name>.<tailnet>.ts.net`. Auch hier muss der Mac wach sein.
* **Eigener kleiner Server** (immer erreichbar, auch für ChatGPT): Dann läuft der Agent
  gleich mit auf dem Server, und der Mac wird gar nicht mehr gebraucht. Anleitung:
  [`SERVER_START.md`](SERVER_START.md).

Wechsel von Tailscale zu einer anderen Relay-Adresse:

```bash
healthbridge onboard --rotate --relay https://<neue-adresse>
```

Danach in der App unter Settings **Unpair this iPhone** wählen und den neuen QR-Code
scannen.

## 5. Mit Claude Code verbinden

```bash
claude mcp add healthbridge -- healthbridge mcp
```

(Läuft der Agent auf dem Server, stattdessen die Connector-Adresse verwenden:
`claude mcp add --transport http healthbridge https://…/mcp/…`, siehe
[`SERVER_START.md`](SERVER_START.md), Schritt 6c.)

Dann zum Beispiel fragen: „Wie habe ich diese Woche geschlafen?“ oder „Vergleiche meine
Ruhepuls-Werte mit dem Vormonat.“ Codex und andere Agenten: siehe `integrations/`.

## 6. Für Freunde

* Jeder Freund braucht einen **eigenen Agenten** (auf seinem Rechner oder einem eigenen
  Server). Die Gesundheitsdaten sind für genau diesen Agenten verschlüsselt. Würde ein
  Freund mit deinem QR-Code koppeln, landeten seine Daten bei dir.
* Ein Relay aus Variante B kann für alle gemeinsam laufen. Für ein Relay im Internet ein
  Registrierungs-Token setzen (`healthbridge-relay --registration-token <geheim>`). Freunde
  übergeben es beim Onboarding mit `--relay-token <geheim>`.
* Die App selbst: mit kostenloser Apple-ID nur per Kabel an deinem Mac (7 Tage). Mit
  Developer Program über **TestFlight** (App Store Connect → App anlegen → Build per
  Xcode → Product → Archive hochladen → Tester einladen). Für TestFlight brauchst du die
  vorbereiteten Texte in `docs/store/`.

## Später: App-Store-Version

Die Abo-Logik („HealthBridge Pro“) ist im Code vorhanden, aber nur in der
**Store-Edition** aktiv. Wie man sie baut, steht in `ios/README.md` im Abschnitt
„Store-Edition“. Store-Texte und Datenschutz-Entwürfe liegen in `docs/store/`.
