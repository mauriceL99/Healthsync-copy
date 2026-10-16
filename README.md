# HealthBridge: Apple Health für KI-Agenten

HealthBridge bringt Apple-Health-Daten vom iPhone **Ende-zu-Ende-verschlüsselt** zu
deinem KI-Agenten: Claude Code, Codex, OpenClaw, Hermes oder jedes andere Tool mit
MCP- bzw. Skill-Unterstützung. Der Agent erstellt daraus Tageszusammenfassungen,
Trends, Workout-Analysen und Dashboards. Dazu kommen auf Wunsch die **Trainingsdateien
einer Garmin-Uhr** (FIT): Runden, Leistung, Laufdynamik, Kraftsätze, HYROX-Abschnitte,
Trainingsbelastung und Rekorde ([`docs/GARMIN.md`](docs/GARMIN.md)).

Das Projekt ist ein unabhängiger Nachbau des Konzepts der App
**„Health Sync for AI Agents“** von Lukas Benedikt Osterheider. Die Analyse des
Originals ist mit [REA](https://github.com/morluto/rea) entstanden und in
[`docs/ANALYSIS.md`](docs/ANALYSIS.md) dokumentiert. Der Code ist komplett neu
geschrieben. Das Protokoll ist eigenständig und nicht mit dem Relay des Originals
kompatibel. Name und Branding sind bewusst andere.

```text
 iPhone-App (HealthKit)          Relay (Mailbox, SQLite)          healthbridge (CLI · MCP · Skill)
 ──────────────────────          ───────────────────────          ────────────────────────────────
        ◄──────────── QR-Code: Relay-URL + Agent-Public-Keys + Pairing-Secret ─────────────
 Health-Daten pro Tag
 verschlüsseln (X25519 +
 HKDF + ChaCha20-Poly1305) ──►   speichert nur Ciphertext
                                                         ◄──────  signierter Abruf (Ed25519)
                                 liefert Envelopes ─────────────► entschlüsseln, validieren,
                                 löscht nach Ack                  SQLite, Summary, Dashboard,
                                                                  MCP-Tools für den Agenten
```

## Komponenten

| Verzeichnis | Inhalt |
| --- | --- |
| [`ios/`](ios/) | SwiftUI-App: Pairing per QR, Metrikauswahl, HealthKit-Auslese, Verschlüsselung mit CryptoKit, Sync manuell, per Zeitplan oder bei neuen Health-Daten. Standard ist die persönliche Edition (alles freigeschaltet); ein Pro-Abo über StoreKit 2 gibt es nur in der Store-Edition |
| [`cmd/healthbridge`](cmd/healthbridge) | Agent-CLI: `onboard`, `fetch`, `summary`, `series`, `workouts`, `dashboard`, `export`, `mcp`, … |
| [`internal/training`](internal/training) | FIT-Dateien von Sportuhren lesen und auswerten: Bestleistungen, Entkopplung, Kraftsätze mit 1RM, HYROX-Abschnitte, Belastung (akut/chronisch, Fitness/Form), Rekorde. GPS wird nie ausgegeben |
| [`cmd/healthbridge-relay`](cmd/healthbridge-relay) | Selbst hostbarer Relay-Server (eine Go-Binary, SQLite, Docker + Caddy) |
| [`integrations/`](integrations/) | Claude-Code-Plugin (MCP + Skill + `/health`), Codex, OpenClaw, generischer Skill |
| [`docs/`](docs/) | [Protokoll](docs/PROTOCOL.md), [Datenschema](docs/DATA_SCHEMA.md), [REA-Analyse](docs/ANALYSIS.md) |
| [`testdata/protocol_vectors.json`](testdata/protocol_vectors.json) | Krypto-Testvektoren, gemeinsam für Go und Swift, zusätzlich mit Python `cryptography` gegengeprüft |

## Schnellstart

> **Schritt für Schritt, ohne Vorkenntnisse:**
> - App aufs iPhone und Agent auf dem Mac (Claude Code): [`docs/MAC_START.md`](docs/MAC_START.md)
> - Agent rund um die Uhr auf einem kleinen Server, nutzbar mit **ChatGPT**, Claude
>   (Browser und Apps) und Claude Code: [`docs/SERVER_START.md`](docs/SERVER_START.md)
> - Trainings einer Garmin-Uhr (z. B. fenix 8) dazuholen: [`docs/GARMIN.md`](docs/GARMIN.md)

### 1. Bauen

```bash
make build            # → bin/healthbridge, bin/healthbridge-relay  (Go ≥ 1.24, kein CGO)
make test             # alle Tests inkl. End-to-End mit Relay
make e2e              # Demo ohne iPhone: Relay + simuliertes Gerät + Summary
```

### 2. Relay betreiben

```bash
# lokal / im LAN (http ist nur für localhost und private Netze erlaubt)
bin/healthbridge-relay --addr :8787 --db relay.db

# nur das Relay öffentlich mit automatischem HTTPS; nur wer das Token kennt, kann Accounts anlegen
HB_DOMAIN=relay.example.com HB_RELAY_REGISTRATION_TOKEN=$(openssl rand -hex 16) \
  docker compose -f deploy/docker-compose.yml up -d relay caddy

# Relay und Agent zusammen auf einem Server (für ChatGPT/Claude), alles geführt:
sudo ./deploy/setup.sh        # Anleitung: docs/SERVER_START.md
```

Wenn das Relay ein Token verlangt, gibst du es beim Onboarding mit
`--relay-token …` (oder über `HEALTHBRIDGE_RELAY_TOKEN`) an.

Das Relay sieht nur Ciphertext, Public Keys und Metadaten (Zeitpunkte, Größen).
Abgeholte Daten löscht es nach dem Ack, nicht abgeholte nach 30 Tagen.

### 3. Agent einrichten und iPhone koppeln

```bash
bin/healthbridge onboard --relay https://relay.example.com --name "MacBook"
```

Der Befehl zeigt einen QR-Code, speichert `~/.healthbridge/pairing-qr.png` und gibt
einen **Sicherheitscode** aus. In der iPhone-App wählst du **Pair with agent**, scannst
den Code und vergleichst den Sicherheitscode. Danach wählst du die Metriken, erlaubst
den Health-Zugriff und tippst auf **Sync now**. Anschließend:

```bash
bin/healthbridge fetch
bin/healthbridge summary --period week
bin/healthbridge dashboard --days 30        # → ~/.healthbridge/dashboard.html
```

Ohne iPhone kannst du alles mit synthetischen Daten testen:
`bin/healthbridge dev simulate-device --days 60`.

### 4. Mit dem Agenten verbinden

```bash
claude mcp add healthbridge -- healthbridge mcp          # Claude Code (nur MCP)
# oder als Plugin inkl. Skill und /health-Befehl:
#   /plugin marketplace add mauriceL99/Healthsync-copy
#   /plugin install healthbridge@healthbridge
codex mcp add healthbridge -- healthbridge mcp           # Codex
```

Läuft der Agent als Dienst auf einem Server (`healthbridge serve`, siehe
[`docs/SERVER_START.md`](docs/SERVER_START.md)), verbinden sich ChatGPT, Claude (Browser
und Apps) und Claude Code über die geheime Connector-Adresse
(`healthbridge connector-url`):

```bash
claude mcp add --transport http healthbridge https://…/mcp/…   # Claude Code
```

In ChatGPT und auf claude.ai trägst du dieselbe Adresse als eigenen Connector ein.

MCP-Tools: `health_summary`, `health_metric_series`, `health_workouts`,
`health_list_metrics`, `health_status`, `health_sync` (Apple Health) sowie
`training_activities`, `training_activity`, `training_load`, `training_records`
(Garmin-Trainings). Details stehen unter [`integrations/`](integrations/).

## CLI-Übersicht

| Befehl | Zweck |
| --- | --- |
| `onboard [--relay URL] [--relay-token T] [--name N] [--rotate] [--new-pairing-code] [--offline]` | Identität anlegen bzw. wiederverwenden, am Relay registrieren, QR zeigen |
| `pairing` | QR-Code und Pairing-Code erneut anzeigen |
| `status [--offline] [--json]` | Relay-Erreichbarkeit, gekoppeltes iPhone, offene Uploads, lokale Daten |
| `fetch [--keep-on-relay] [--json]` | abholen, entschlüsseln, validieren, speichern, am Relay löschen |
| `summary [--period day\|week\|month\|quarter\|year] [--days N] [--end DATE] [--format text\|markdown\|json] [--out FILE]` | Zusammenfassung mit Trends gegenüber der Vorperiode |
| `series <metric> [--days 30]` | Tageswerte einer Metrik, z. B. `heart.hrv_sdnn_ms` |
| `workouts [--days 14] [--samples]` | Workouts mit Herzfrequenz-Zonen, Erholung und Samples |
| `metrics` | alle Metriken und wie viele Tage Daten haben |
| `dashboard [--days 30] [--out FILE]` | eigenständiges HTML-Dashboard (Hell/Dunkel, ohne externe Ressourcen) |
| `export [--format csv\|ndjson]` | Rohdaten-Export |
| `unlink [--delete-relay-account]` | iPhone entkoppeln (lokale Daten bleiben erhalten) |
| `mcp` | MCP-Server über stdio |
| `serve [--addr :8788] [--fetch-every 15m]` | Dienst: holt regelmäßig ab, beantwortet MCP über HTTP unter `/mcp/<geheim>` und nimmt Garmin-Dateien unter `/upload/<geheim>` an |
| `connector-url [--public-url URL] [--rotate] [--upload]` | geheime Connector-Adresse für `serve` anzeigen bzw. neu erzeugen; `--upload`: Adresse der Upload-Seite für Garmin-Dateien |
| `import [--json] DATEI_ODER_ORDNER…` | Garmin-Trainings importieren: `.fit`, `.fit.gz`, ZIP aus Garmin Connect, Konto-Export (auch verschachtelt), Ordner |
| `activities [--days 30] [--kind run\|bike\|strength\|hyrox\|…]` | importierte Trainings mit Pace, Leistung, Puls, Belastung, Training Effect |
| `activity [ID\|latest] [--kind K] [--samples SEK]` | Einzelanalyse: Runden, Zonen, Bestleistungen, Entkopplung, Kraftsätze, HYROX |
| `training [--days 84]` | Wochenumfang, akute/chronische Belastung, Fitness/Form, Intensitätsverteilung |
| `records [--days 365]` | persönliche Rekorde (Zeiten, Watt, 1RM, HYROX) |
| `delete-activity [--samples] [ID…]` | importierte Trainings löschen |
| `self-test` | Offline-Selbsttest |
| `dev simulate-device` | simuliertes iPhone mit synthetischen Daten |
| `dev sample-fit --out ORDNER` | drei synthetische Garmin-Trainings (Lauf, Kraft, HYROX) zum Ausprobieren |

Der Zustand liegt in `~/.healthbridge` (alternativ `$HEALTHBRIDGE_HOME` oder
`--state-dir`). Verzeichnisse haben die Rechte `0700`, Dateien `0600`. Private
Schlüssel und entschlüsselte Daten liegen unverschlüsselt auf der Platte, deshalb
solltest du die Festplattenverschlüsselung aktivieren.

## Sicherheitsmodell (Kurzfassung)

* **Ende-zu-Ende:** Das iPhone verschlüsselt pro Upload mit einem ephemeren X25519-Key
  für den Agent-Public-Key. Die AAD bindet Account, Envelope-ID, Art, Kompression und
  Zeitstempel.
* **Authentisierung:** Jede Anfrage ist mit Ed25519 signiert (Methode, Pfad,
  Body-Hash, Zeitstempel ±5 min, Nonce mit Replay-Schutz). Das iPhone hat einen eigenen
  Geräteschlüssel.
* **Pairing:** Der QR-Code enthält die Relay-URL, die Agent-Public-Keys und ein
  32-Byte-Pairing-Secret. Das Relay speichert nur dessen Hash. Der Sicherheitscode
  schützt vor vertauschten QR-Codes.
* **Prompt-Injection-Schutz:** Entschlüsselte Daten werden gegen eine Allowlist geprüft
  (bekannte Keys, Wertebereiche, String-Regexes). Freitext erreicht den Agenten nie,
  und jede Ausgabe ist als Daten, nicht als Anweisung, gekennzeichnet.
* **Server-Modus (`serve`):** Der Agent entschlüsselt dann auf dem eigenen Server statt
  auf dem eigenen Rechner. Die Connector-Adresse enthält ein 256-Bit-Geheimnis und ist
  der einzige Zugangsschutz. Über `/mcp/` lassen sich Daten nur lesen, über die
  Upload-Seite (`/upload/`, gleiches Geheimnis) zusätzlich Trainings hinzufügen, aber
  nichts ändern oder löschen. Das Geheimnis wird nicht protokolliert und lässt sich mit
  `connector-url --rotate` sofort ersetzen.
* **Garmin-Dateien:** Die Originaldateien (mit GPS-Strecke) bleiben auf dem Rechner bzw.
  Server des Agenten. Die Werkzeuge geben nie GPS-Positionen aus.
* **Kein Medizinprodukt:** HealthBridge ist für Wellness und Automatisierung gedacht,
  nicht für Diagnosen.

## Entwicklung

```bash
make lint test                 # gofmt, vet, Tests mit -race
make vectors                   # Testvektoren neu erzeugen und nach ios/ kopieren
make dist                      # Release-Archive für macOS, Linux und Windows
```

Die iOS-App wird mit XcodeGen erzeugt (`cd ios && xcodegen`). Siehe [`ios/README.md`](ios/README.md).

## Rechtliches

* Unabhängiges Projekt, nicht verbunden mit Lukas Benedikt Osterheider, Apple oder Anthropic.
* Für die Analyse wurden nur öffentlich verfügbare Artefakte untersucht. Das
  Companion-Repository des Originals hat keine Lizenzdatei. Deshalb wurde daraus
  **kein Code übernommen**, nur Architektur und Konzepte.
* Vor einer Veröffentlichung im App Store brauchst du eine eigene Bundle-ID, eine
  Datenschutzerklärung und die HealthKit-Begründungen. Wähle außerdem eine Lizenz für
  dieses Repository.
