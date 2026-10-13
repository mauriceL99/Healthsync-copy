# Analyse: „Health Sync for AI Agents“ (Lukas Benedikt Osterheider)

Diese Analyse beschreibt, wie das Original funktioniert. Sie ist die Grundlage für
den unabhängigen Nachbau **HealthBridge** in diesem Repository. Untersucht wurden
nur öffentlich verfügbare Artefakte. Code aus dem Original wurde nicht übernommen.

## Untersuchte Quellen

| Quelle | Inhalt | Werkzeug |
| --- | --- | --- |
| App-Store-Eintrag `id6759522298` (über Websuche) | Funktionsumfang, Preismodell, Datenschutzangaben | Websuche |
| Öffentliches Companion-Repo `lukasosterheider/apple-health-for-ai-agents` | Go-CLI, Skills, Plugin-Manifeste | Code-Lektüre |
| Release-Binary `apple-health-sync-runtime-2.0.1-linux-x64` | ausgeliefertes CLI-Executable | **REA** (`inspect-artifact`, `inspect-go-binary`) |

Die iOS-App selbst (IPA) ist im App Store FairPlay-verschlüsselt und war in dieser
Umgebung nicht abrufbar (`apps.apple.com` ist durch die Netzwerk-Policy gesperrt).
Ihr Verhalten wurde aus dem Protokoll abgeleitet, das die Gegenseite (CLI) erwartet.

## REA-Befunde zum ausgelieferten Binary

REA wurde aus dem Quellcode gebaut (`npm ci && npm run build`, Version 6.4.0 + `main`),
weil `inspect-go-binary` noch nicht im npm-Release enthalten ist.

```text
rea inspect-artifact  …/healthsync
  format: elf, size 11 677 856 B, sha256 dec85698…dfb9 (= executableSha256 im Manifest)
  executable: true, encrypted: false, keine Container-Einträge
  next_probe: open_binary (benötigt Hopper/Ghidra/IDA – hier nicht installiert)

rea inspect-go-binary …/healthsync
  go_version: go1.27.0
  path: github.com/lukasosterheider/apple-health-for-ai-agents/cli/cmd/healthsync
  deps: golang.org/x/crypto v0.55.0, golang.org/x/sys v0.47.0, modernc.org/sqlite v1.57.0, …
  build: -trimpath=true, CGO_ENABLED=0, GOOS=linux, GOARCH=amd64, GOAMD64=v1
```

**Schlussfolgerung:** Das ausgelieferte Binary wurde ohne CGO aus genau dem
öffentlichen Modul mit denselben Abhängigkeiten gebaut. Die Protokollanalyse aus dem
Quellcode beschreibt also das tatsächlich ausgelieferte Verhalten. Die SHA-256-Summe
stimmt mit dem im Repo gepinnten Manifest überein.

## Architektur des Originals

```text
iPhone-App (HealthKit)                Relay (Supabase Edge Functions, eu-west-1)        Agent/CLI
───────────────────────               ─────────────────────────────────────────        ──────────
QR scannen  ───────────────────────►  Gerät verknüpfen                         ◄────── onboard: Schlüssel erzeugen,
Health-Daten lesen                                                                      QR vom Relay rendern lassen
pro Tag JSON bauen
mit X25519-Pubkey des Agents
verschlüsseln  ────────────────────►  verschlüsselte Zeilen speichern
                                      (scope = history | recent)
                                                                  ◄───────────────────  fetch: Challenge holen,
                                                                                        mit Ed25519 signieren
                                      Zeilen ausliefern ─────────────────────────────►  lokal entschlüsseln,
                                                                                        validieren, in SQLite
                                                                                        speichern, zusammenfassen
```

### Identität und Pairing

* Der Agent erzeugt ein **Ed25519**-Signaturschlüsselpaar und ein
  **X25519**-Verschlüsselungsschlüsselpaar sowie eine zufällige User-ID (`ahs_…`).
* Das Pairing-Payload (QR-Code oder Hex-String) enthält nur öffentliche Daten:
  Protokollversion, User-ID, beide Public Keys und einen SHA-256-Fingerprint.
* Das QR-Bild wird vom Relay gerendert (signierte Anfrage), offline gibt es den Hex-String.
* Private Schlüssel und entschlüsselte Daten liegen unverschlüsselt (nur per
  Dateirechten `0600/0700` geschützt) in `~/.apple-health-sync`.

### Verschlüsselung (v5)

* Pro Datensatz ephemeres X25519-Schlüsselpaar auf dem iPhone, ECDH mit dem
  Agent-Public-Key, **HKDF-SHA256** → 32-Byte-Schlüssel, **ChaCha20-Poly1305**
  mit 12-Byte-Nonce.
* Die AAD bindet `scope` und `user_id`. Ein `kid` (SHA-256 des Empfänger-Public-Keys)
  adressiert den Schlüssel.
* Größenlimits: 4 MiB für `recent`, 16 MiB für `history`.

### Relay-Authentifizierung

* Challenge/Response: Der Agent holt eine Challenge, signiert sie mit Ed25519 und sendet
  sie zusammen mit dem Public Key. Nur dann liefert das Relay die verschlüsselten Zeilen.
* Endpunkte: QR-Rendering, Daten abholen, Gerät entkoppeln. Die Relay-URLs sind
  fest einkompiliert, Redirects werden abgelehnt, TLS ≥ 1.2 mit gebündelten CA-Roots.

### Datenmodell

* Klartext ist ein JSON-Objekt `{ "YYYY-MM-DD": { …Metriken… } }`.
* `history` (Backfill) wird zuerst angewendet, `recent` überschreibt pro Tag und Schlüssel.
* Workouts enthalten u. a. `heart_rate_samples`, `workout_events`, `speed_samples`,
  `distance_intervals`, `route_points` und `workout_timing` (Offsets in ms).
* Eingehende Daten gelten als **nicht vertrauenswürdig**. Strings werden per Allowlist
  und Regex validiert, Zahlen auf Endlichkeit geprüft, Tiefe und Knotenanzahl sind begrenzt.
  Das ist wichtig, weil die Daten später in den Kontext eines LLM-Agents gelangen
  (Schutz vor Prompt-Injection).
* Gespeichert wird lokal in SQLite (`health_data(user_id, date, data)`) oder NDJSON.

### Zusammenfassungen

* `summary --period daily|weekly|monthly` flacht alle numerischen Werte zu Pfaden ab
  (`heart.rate[]`) und berechnet `count/min/max/avg/latest`. Ausgabe als Text oder JSON.

### Agent-Integrationen

* Dasselbe Go-Binary steckt in einem Claude-Code-Plugin, einem Codex-Plugin, einem
  OpenClaw-Skill und einem generischen Skill. Ein Launcher lädt das Binary
  checksum-gepinnt aus den GitHub-Releases.
* Der Skill erlaubt Aktionen nur auf ausdrücklichen Wunsch. Installation allein
  löst kein Pairing, Fetching oder Scheduling aus.

### App-Funktionen laut Store-Eintrag

* Metriken pro Kategorie auswählbar.
* Verschlüsselung auf dem Gerät vor dem Upload.
* Sync manuell, nach Zeitplan oder bei neuen Health-Daten
  (HealthKit Background Delivery).
* Kostenlos mit In-App-Käufen.
* Neueste Version: detaillierte Herzfrequenz-Metriken für Workouts.
* Positionierung: Wellness und Automatisierung, keine medizinische Diagnose.

## Was HealthBridge bewusst anders macht

| Thema | Original | HealthBridge |
| --- | --- | --- |
| Relay | proprietär, Supabase, fest einkompiliert | eigener, selbst hostbarer Go-Relay (SQLite), URL im Pairing-Code |
| QR-Code | vom Relay gerendert | lokal im Terminal bzw. als PNG gerendert, kein Netzwerk nötig |
| Auth | Challenge-Roundtrip pro Anfrage | signierte Requests (Timestamp + Nonce + Body-Hash), Replay-Schutz |
| Abholen | immer alle Zeilen | Mailbox mit Cursor und Ack, Relay löscht nach Abholung |
| Gerätebindung | – | Pairing-Secret im QR, Relay speichert nur den Hash |
| Agent-Zugriff | CLI + Skills | CLI + Skills **+ MCP-Server** (`healthbridge mcp`) |
| Auswertung | generische Statistik | Statistik je Metrik, Trend gegenüber der Vorperiode, Schlaf-/Workout-Auswertung, HTML-Dashboard |
| Tests ohne iPhone | – | `healthbridge dev simulate-device` erzeugt synthetische Daten end-to-end |
