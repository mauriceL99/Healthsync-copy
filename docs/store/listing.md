# Store-Eintrag und TestFlight – Entwurf

Texte für App Store Connect (Deutsch und Englisch). Längen sind geprüft. Vor dem
Anlegen prüfen, ob der Name „HealthBridge“ im App Store noch frei ist.

## Stammdaten

| Feld | Wert |
| --- | --- |
| Name (max. 30) | HealthBridge |
| Untertitel DE (max. 30) | Health-Daten für KI-Agenten |
| Untertitel EN (max. 30) | Health data for AI agents |
| Primäre Kategorie | Gesundheit & Fitness |
| Sekundäre Kategorie | Entwicklertools |
| Altersfreigabe | 4+ (keine der Inhaltskategorien zutreffend) |
| Support-URL | [z. B. GitHub-Repository oder eigene Seite] |
| Datenschutz-URL | [veröffentlichte `datenschutz.md` / `privacy-policy.md`] |
| Preis | kostenlos; Pro nur in der Store-Edition |

## Schlüsselwörter (max. 100 Zeichen, ohne fremde Markennamen)

* DE (88): `ki,agent,mcp,gesundheit,export,schlaf,puls,schritte,training,hrv,verschlüsselt,llm,daten`
* EN (85): `ai,agent,mcp,health,export,sleep,heart rate,steps,workout,hrv,encrypted,llm,data,sync`

## Beschreibung DE

> Gib deinem KI-Agenten Zugriff auf deine Gesundheitsdaten, Ende-zu-Ende-verschlüsselt
> und unter deiner Kontrolle.
>
> HealthBridge liest die Health-Daten, die du auswählst: Aktivität, Herz, Schlaf,
> Körper, Vitalwerte, Ernährung, Achtsamkeit und Workouts mit Herzfrequenzzonen. Die App
> verschlüsselt sie direkt auf dem iPhone für deinen eigenen Agenten. Der Agent läuft
> als kleines Kommandozeilenprogramm auf deinem Rechner und stellt die Daten KI-Assistenten
> wie Claude Code oder Codex über MCP zur Verfügung. Frag zum Beispiel: „Wie habe ich
> diese Woche geschlafen?“ oder „Wie hat sich mein Ruhepuls entwickelt?“
>
> • Ende-zu-Ende-verschlüsselt (X25519, ChaCha20-Poly1305). Der Relay-Server sieht nur
>   unlesbare Daten.
> • Koppeln per QR-Code, mit Sicherheitscode zum Vergleichen
> • Du wählst jede Kategorie einzeln. Nicht gewählte Daten werden nie gelesen.
> • Sync per Knopfdruck, nach Zeitplan oder bei neuen Health-Daten
> • Keine Werbung, kein Tracking, keine Analyse-SDKs
> • Relay selbst betreibbar, Quelltext offen
>
> HealthBridge ist kein Medizinprodukt und ersetzt keine ärztliche Beratung.

## Description EN

> Give your AI agent access to your health data, end-to-end encrypted and under your
> control.
>
> HealthBridge reads the Health data you choose: activity, heart, sleep, body, vitals,
> nutrition, mindfulness and workouts with heart-rate zones. It encrypts everything on
> your iPhone for your own agent, a small command-line program on your computer that makes
> the data available to AI assistants such as Claude Code or Codex through MCP. Ask
> things like "How did I sleep this week?" or "How has my resting heart rate changed?"
>
> • End-to-end encrypted (X25519, ChaCha20-Poly1305); the relay server only sees
>   unreadable data
> • Pair with a QR code and compare a safety code
> • Choose every category yourself; unselected data is never read
> • Sync on demand, on a schedule or when new Health data arrives
> • No ads, no tracking, no analytics SDKs
> • Self-hostable relay, open source
>
> HealthBridge is not a medical device and does not replace medical advice.

## Screenshots

Eine Größe genügt, App Store Connect skaliert für kleinere iPhones. Aktuell verlangt
Apple die größte iPhone-Größe (6,9″, 1320 × 2868 px). Vor dem Hochladen prüfen.
Aufnahme: Simulator „iPhone 16 Pro Max“ → ⌘S.

1. Startseite nach einem Sync (letzter/nächster Sync, „Encrypted“)
2. Kategorienauswahl im Onboarding
3. Koppeln: Sicherheitscode-Bestätigung
4. Metrik-Liste mit Workout-Herzfrequenzdetails
5. Aktivitätsprotokoll (zeigt, dass nur verschlüsselte Pakete gesendet werden)

## Hinweise für die App-Prüfung

Die App braucht einen gekoppelten Agenten. Damit Prüfer sie testen können:

1. Ein Demo-Relay mit HTTPS betreiben und einen Demo-Agenten anlegen:
   `healthbridge --state-dir ~/hb-review onboard --relay https://… --name "Review"`.
2. Den ausgegebenen Code `HB1:…` in die Prüfnotizen kopieren. Die App kann ihn statt des
   QR-Codes einfügen (Schritt „Pair with your agent“ → Paste).
3. Notiztext (EN):
   > HealthBridge sends the Health data the user selects, end-to-end encrypted, to the
   > user's own AI agent. To test: Get started → Pair with your agent → Paste the pairing
   > code below → confirm the safety code → choose categories → allow Health access →
   > Sync now. The data is encrypted for our demo agent and deleted after review.
   > Pairing code: HB1:…
4. Nach der Prüfung: `healthbridge --state-dir ~/hb-review unlink --delete-relay-account`.

## TestFlight (Freunde)

| Feld | Inhalt |
| --- | --- |
| Beta-App-Beschreibung | erster Absatz der Beschreibung oben |
| Feedback-E-Mail | [E-Mail] |
| Datenschutz-URL | Pflicht für externe Tester |
| Was soll getestet werden | „Koppeln mit deinem eigenen Agenten (Anleitung: docs/MAC_START.md), Sync, Hintergrund-Sync über Nacht“ |

Externe Tester (per öffentlichem Link) erfordern eine kurze Beta-Prüfung durch Apple.
Interne Tester (bis 100, müssen Mitglied deines App-Store-Connect-Teams sein) brauchen
keine.
