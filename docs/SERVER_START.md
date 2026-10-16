# HealthBridge rund um die Uhr – mit eigenem kleinem Server

Diese Anleitung richtet HealthBridge so ein, dass es **ohne deinen Mac** läuft und du
deine Gesundheitsdaten mit **ChatGPT** oder **Claude** (im Browser, in der Desktop-App
und in Claude Code) nutzen kannst. Sie setzt keine Vorkenntnisse voraus: Jeder Befehl
steht da, und du kannst ihn abtippen oder kopieren.

**Was du am Ende hast**

```
iPhone (HealthBridge-App)
   │  verschlüsselt, über das Internet
   ▼
dein Server  ── Relay: nimmt die verschlüsselten Pakete an
             ── Agent: entschlüsselt sie, speichert sie, beantwortet Fragen
   │  geheime HTTPS-Adresse („Connector-Adresse“)
   ▼
ChatGPT · Claude · Claude Code
```

| | |
| --- | --- |
| Zeit | etwa 45 Minuten (das meiste ist Warten) |
| Kosten | ein kleiner Mietserver, je nach Anbieter etwa 4–7 € im Monat, monatlich kündbar |
| Du brauchst | einen Computer (Mac oder Windows), ein Zahlungsmittel für den Server-Anbieter, das iPhone mit der HealthBridge-App (Installation: [`MAC_START.md`](MAC_START.md), Schritte 1–3) und ein ChatGPT-Abo (Plus oder höher) oder ein Claude-Konto |

> **Bitte vorher lesen: Was ändert sich beim Datenschutz?**
> In dieser Variante entschlüsselt der Agent deine Daten **auf dem gemieteten Server**.
> Das ist dein eigener Server, aber er steht im Rechenzentrum des Anbieters. Außerdem
> kann **jeder, der die Connector-Adresse kennt, deine Gesundheitsdaten lesen**. Behandle
> sie also wie ein Passwort. ChatGPT bzw. Claude bekommen bei jeder Frage die Werte zu
> sehen, die sie dafür abrufen. Wer das nicht möchte, lässt den Agenten nur auf dem
> eigenen Mac laufen ([`MAC_START.md`](MAC_START.md), Schritt 4). Dann geht aber nur
> Claude Code, und der Mac muss eingeschaltet sein.

---

## Schritt 1: Einen SSH-Schlüssel erstellen

Mit einem SSH-Schlüssel meldest du dich sicher am Server an. Er besteht aus zwei
Teilen: dem **privaten Schlüssel**, der auf deinem Computer bleibt und nie weitergegeben
wird, und dem **öffentlichen Schlüssel**, den du gleich beim Server-Anbieter hinterlegst.

**Auf dem Mac**

1. Das Programm **Terminal** öffnen: ⌘ + Leertaste drücken, „Terminal“ eintippen, Enter.
2. Diesen Befehl eingeben und Enter drücken:
   ```bash
   ssh-keygen -t ed25519 -C "healthbridge"
   ```
3. Die Fragen dreimal mit Enter bestätigen. Wenn du möchtest, kannst du bei der Frage
   nach einer *passphrase* ein Passwort für den Schlüssel vergeben.
4. Den öffentlichen Schlüssel anzeigen:
   ```bash
   cat ~/.ssh/id_ed25519.pub
   ```
   Es erscheint eine Zeile, die mit `ssh-ed25519` beginnt. Markiere die **ganze Zeile**
   und kopiere sie (⌘ + C).

**Auf Windows 10/11**

1. **PowerShell** öffnen: Start-Menü, „PowerShell“ eintippen, Enter.
2. Eingeben und Enter drücken:
   ```powershell
   ssh-keygen -t ed25519 -C "healthbridge"
   ```
3. Die Fragen dreimal mit Enter bestätigen.
4. Den öffentlichen Schlüssel anzeigen und kopieren:
   ```powershell
   Get-Content $env:USERPROFILE\.ssh\id_ed25519.pub
   ```

Lass das Fenster offen. Du brauchst es in Schritt 3 wieder.

## Schritt 2: Einen Server mieten

Die Anleitung nutzt **Hetzner Cloud** (deutscher Anbieter, Rechenzentren in
Deutschland). Jeder andere Anbieter mit Ubuntu-Servern funktioniert genauso.

1. Auf [hetzner.com/cloud](https://www.hetzner.com/cloud) ein Konto anlegen und die
   E-Mail-Adresse bestätigen. Hetzner fragt nach einem Zahlungsmittel und prüft neue
   Konten manchmal kurz (zum Beispiel per Ausweis). Das kann ein paar Minuten dauern.
2. In der **Cloud Console** auf **„+ Neues Projekt“** klicken, als Namen
   `HealthBridge` eingeben und das Projekt öffnen.
3. Auf **„Server hinzufügen“** klicken und Folgendes auswählen:

   | Feld | Auswahl |
   | --- | --- |
   | Standort | Nürnberg oder Falkenstein (Deutschland) |
   | Image | **Ubuntu 24.04** |
   | Typ | „Shared vCPU“, der **kleinste** angebotene Typ. x86 oder Arm ist egal, 2 GB Arbeitsspeicher reichen |
   | Netzwerk | **Öffentliche IPv4 eingeschaltet lassen** (kostet einen kleinen Aufpreis, wird aber gebraucht) |
   | SSH-Keys | „SSH-Key hinzufügen“, die kopierte Zeile aus Schritt 1 einfügen, Name z. B. „Mein Mac“ |
   | Firewalls | keine auswählen |
   | Name | `healthbridge` |

4. Auf **„Kostenpflichtig erstellen“** klicken. Nach etwa einer Minute ist der Server
   da. **Notiere seine IPv4-Adresse**, zum Beispiel `203.0.113.5`.

## Schritt 3: Mit dem Server verbinden

Im Terminal bzw. in der PowerShell aus Schritt 1 eingeben, dabei die IP-Adresse durch
deine ersetzen:

```bash
ssh root@203.0.113.5
```

Beim ersten Mal kommt die Frage `Are you sure you want to continue connecting
(yes/no/[fingerprint])?`. Tippe `yes` und drücke Enter. Danach siehst du eine Zeile wie
`root@healthbridge:~#`. Alles, was du jetzt eintippst, läuft **auf dem Server**.
Mit `exit` verlässt du den Server wieder.

## Schritt 4: HealthBridge installieren

Auf dem Server diese drei Zeilen nacheinander eingeben. Nach jeder Zeile Enter drücken
und warten, bis wieder `root@healthbridge:~#` erscheint:

```bash
apt-get update && apt-get install -y git
git clone https://github.com/mauriceL99/Healthsync-copy.git
cd Healthsync-copy && ./deploy/setup.sh
```

Das Setup-Skript stellt zwei Fragen:

1. **Adresse:** Einfach **Enter** drücken. Das Skript schlägt eine kostenlose Adresse wie
   `203-0-113-5.sslip.io` vor, die auf deinen Server zeigt. Wer eine eigene Domain hat,
   kann sie hier eintragen. Dann muss vorher ein A-Eintrag auf die Server-IP gesetzt
   sein.
2. **Name:** Wie der Server in der iPhone-App heißen soll. Enter übernimmt „HealthBridge
   Server“.

Danach installiert das Skript alles Nötige, holt ein HTTPS-Zertifikat und richtet den
Agenten ein. Beim ersten Mal dauert das **5–10 Minuten**. Am Ende siehst du:

- einen **QR-Code** aus Kästchen. Mach das Fenster groß genug, damit er vollständig
  sichtbar ist.
- den **Sicherheitscode**, z. B. `F184-81AE-67D7`.
- den **Pairing-Code** (eine lange Zeile, die mit `HB1:` beginnt).
- die **Connector-Adresse**, z. B. `https://203-0-113-5.sslip.io/mcp/ZR9Z…`.
- die **Upload-Adresse** für Garmin-Trainings, z. B. `https://203-0-113-5.sslip.io/upload/ZR9Z…`
  (Anleitung: [`GARMIN.md`](GARMIN.md)). Sie hat ein eigenes Geheimnis und erlaubt nur das
  Hochladen von Trainings.

Lass das Fenster offen.

## Schritt 5: Das iPhone koppeln

1. Die HealthBridge-App öffnen. War sie schon mit dem Mac gekoppelt: zuerst unter
   **Settings → Unpair this iPhone** entkoppeln.
2. **Get started** tippen und den QR-Code auf dem Bildschirm scannen. Klappt das Scannen
   nicht, den Pairing-Code (`HB1:…`) zum Beispiel per Notiz oder Nachricht an dich
   selbst aufs iPhone bringen und in der App einfügen.
3. Prüfen, ob der **Sicherheitscode** in der App genau mit dem im Terminal
   übereinstimmt, und bestätigen.
4. Datenkategorien auswählen, den Zugriff auf Apple Health erlauben und auf **Sync now**
   tippen.
5. Für automatische Syncs auf der Startseite der App in der Karte **Sync mode**
   „On new data“ (sobald Apple Health neue Werte hat) oder „Scheduled“ (nach Zeitplan)
   wählen.

Der Server holt neue Daten **alle 15 Minuten** selbst ab. Um es sofort zu prüfen, auf
dem Server eingeben:

```bash
./deploy/hb fetch
./deploy/hb status
```

Unter „Local data“ sollte jetzt eine Anzahl Tage stehen.

## Schritt 6: Mit ChatGPT, Claude oder Claude Code verbinden

Du brauchst dafür die **Connector-Adresse** aus Schritt 4. Falls das Fenster schon zu
ist: per `ssh root@<IP>` verbinden und dann eingeben:

```bash
cd Healthsync-copy && ./deploy/hb connector-url
```

Die Adresse am besten direkt aus dem Terminal kopieren und nicht abtippen. Sie ist lang
und muss vollständig sein.

### 6a. ChatGPT

Voraussetzung ist ein **ChatGPT Plus, Pro, Business oder Enterprise** Konto. Mit dem
kostenlosen Konto geht es nicht. Die Einrichtung klappt am zuverlässigsten **im Browser
am Computer** auf [chatgpt.com](https://chatgpt.com).

> OpenAI benennt diese Menüs häufig um. Die Schritte entsprechen dem Stand Oktober 2026.
> Heißt ein Menüpunkt bei dir anders, suche nach „Apps“, „Connectors“ oder „Plugins“.

1. Auf chatgpt.com anmelden. Unten links auf deinen Namen klicken, dann auf
   **Einstellungen**.
2. **Apps & Connectors** öffnen (je nach Version „Connectors“, „Apps“ oder „Plugins“).
3. Ganz unten **Erweiterte Einstellungen** öffnen und den **Entwicklermodus**
   einschalten. Den Hinweis auf das „erhöhte Risiko“ bestätigen. Er bedeutet nur, dass
   du selbst entscheidest, welchen Servern ChatGPT vertraut.
4. Zurück in **Apps & Connectors** auf **Erstellen** klicken (bzw. „Connector
   erstellen“ oder „Plugins → Hinzufügen → MCP-App erstellen“).
5. Das Formular ausfüllen:

   | Feld | Eintrag |
   | --- | --- |
   | Name | `HealthBridge` |
   | Beschreibung | `Meine Apple-Health-Daten` |
   | MCP-Server-URL | deine Connector-Adresse |
   | Authentifizierung | **Keine Authentifizierung** („No authentication“) |

   Den Haken bei „Ich vertraue dieser Anwendung“ setzen und auf **Erstellen** klicken.
   ChatGPT verbindet sich und zeigt zehn Werkzeuge (health_summary, training_activities …).
6. **Benutzen:** Einen neuen Chat öffnen, auf **+** klicken, dann **Mehr** →
   **Entwicklermodus** und **HealthBridge** einschalten. Dann einfach fragen, zum
   Beispiel: *„Wie habe ich diese Woche geschlafen?“*
7. ChatGPT fragt vor dem ersten Abruf, ob es das Werkzeug benutzen darf. Bestätigen.
   Die Abfragewerkzeuge sind als „nur lesend“ markiert. Nur „Fetch new data“ fragt
   jedes Mal nach, weil es neue Daten abholt. Das musst du aber nicht benutzen, der
   Server holt ohnehin alle 15 Minuten ab.

Ob Connectors aus dem Entwicklermodus auch in der ChatGPT-App auf dem iPhone
erscheinen, ändert OpenAI immer wieder. Klappt es dort nicht, nutze chatgpt.com im
Browser.

### 6b. Claude (Browser, Desktop-App und iPhone-App)

1. Auf [claude.ai](https://claude.ai) anmelden. Unten links auf deinen Namen klicken,
   dann auf **Einstellungen** → **Connectors** (direkt: `claude.ai/customize/connectors`).
2. Auf **+** bzw. **„Eigenen Connector hinzufügen“** („Add custom connector“) klicken.
3. Name: `HealthBridge`, URL: deine Connector-Adresse. Die „Erweiterten Einstellungen“
   (OAuth) leer lassen. Auf **Hinzufügen** klicken.
4. **Benutzen:** Im Chat auf das Werkzeug-Symbol bzw. **+** unter dem Eingabefeld
   tippen, **Connectors** öffnen und **HealthBridge** einschalten. Dann fragen, zum
   Beispiel: *„Vergleiche meinen Ruhepuls mit dem Vormonat.“* Fragt Claude nach der
   Erlaubnis, **„Immer erlauben“** wählen.
5. Connectors, die du auf claude.ai hinzufügst, stehen in der Regel auch in der
   Claude-App auf dem iPhone und am Computer zur Verfügung. Dort ebenfalls über das
   Werkzeug-Menü einschalten.

Bei Team- oder Enterprise-Konten muss eventuell ein Admin eigene Connectors erlauben.

### 6c. Claude Code (Terminal am Mac)

```bash
claude mcp add --transport http healthbridge https://DEINE-CONNECTOR-ADRESSE
claude mcp list
```

In `claude mcp list` sollte `healthbridge` als verbunden erscheinen. Danach in Claude
Code einfach nach deinen Daten fragen.

**Gute erste Fragen**

- „Fasse meine Gesundheitsdaten der letzten Woche zusammen.“
- „Wie hat sich meine Schlafdauer im letzten Monat entwickelt?“
- „Welche Workouts hatte ich im September, und wie lange war ich in den einzelnen
  Pulszonen?“
- „Welche Metriken hast du überhaupt von mir?“

**Garmin-Uhr?** Trainings mit Runden, Leistung, Kraftsätzen und HYROX-Abschnitten kommen
über die Upload-Seite dazu. Anleitung: [`GARMIN.md`](GARMIN.md).

## Schritt 7: Im Alltag

Alle Befehle laufen auf dem Server, also vorher `ssh root@<IP>` und
`cd Healthsync-copy`:

| Was | Befehl |
| --- | --- |
| Status ansehen | `./deploy/hb status` |
| Sofort neue Daten holen | `./deploy/hb fetch` |
| Zusammenfassung im Terminal | `./deploy/hb summary --period week` |
| QR-Code erneut anzeigen (neues iPhone) | `./deploy/hb pairing` |
| Connector-Adresse anzeigen | `./deploy/hb connector-url` |
| **Neue** Connector-Adresse (alte sofort ungültig) | `./deploy/hb connector-url --rotate`, danach in ChatGPT/Claude ersetzen |
| **Neue** Upload-Adresse (alte sofort ungültig) | `./deploy/hb connector-url --upload --rotate` |
| HealthBridge aktualisieren | `git pull && ./deploy/setup.sh` |
| Upload-Adresse für Garmin-Dateien anzeigen | `./deploy/hb connector-url --upload` |
| Garmin-Trainings ansehen | `./deploy/hb activities` · `./deploy/hb activity latest` · `./deploy/hb training` |
| Protokolle ansehen | `docker compose -f deploy/docker-compose.yml logs --tail 50 agent` |

Nach einem Neustart des Servers startet HealthBridge von selbst. Ubuntu installiert
Sicherheitsupdates automatisch.

**Server kündigen:** In der Hetzner Cloud Console den Server löschen. Damit sind alle
Daten auf dem Server weg. In der iPhone-App danach **Unpair this iPhone** wählen.

**Sicherung (optional):** Schlüssel und Daten des Agenten liegen in einem
Docker-Volume. So sicherst du sie auf dem Server in eine Datei:

```bash
docker run --rm -v healthbridge_agent-data:/data -v "$PWD":/backup busybox tar czf /backup/agent-backup.tgz -C /data .
```

Die Datei `agent-backup.tgz` enthält entschlüsselte Gesundheitsdaten und die Schlüssel.
Bewahre sie entsprechend sicher auf.

## Wenn etwas hakt

| Problem | Lösung |
| --- | --- |
| `./deploy/setup.sh: No such file or directory` | Der Server-Modus ist noch nicht im Hauptzweig (`main`) auf GitHub. Den offenen Pull Request auf GitHub mergen, dann im Ordner `Healthsync-copy` `git pull` eingeben und `./deploy/setup.sh` erneut starten |
| `Permission denied (publickey)` beim `ssh` | Der Schlüssel aus Schritt 1 wurde beim Server nicht hinterlegt. In der Hetzner Console den Server neu erstellen und dabei den SSH-Key auswählen, oder über „Rescue“ → „Root-Passwort zurücksetzen“ mit Passwort anmelden |
| Setup: „Die Adresse … ist im Internet nicht bekannt“ | Bei eigener Domain fehlt der A-Eintrag oder er ist noch nicht verbreitet (bis zu 1 Stunde warten). Mit Enter die sslip.io-Adresse nehmen |
| Setup: „https://… antwortet nicht“ | Meist blockiert eine Firewall die Ports 80 und 443. In der Hetzner Console unter „Firewalls“ entweder keine Firewall zuweisen oder TCP 22, 80 und 443 erlauben. Danach `./deploy/setup.sh` erneut starten |
| Zertifikat klappt mit sslip.io wiederholt nicht | Selten sind die Kontingente von Let's Encrypt für sslip.io erschöpft. Dann eine eigene Domain oder einen kostenlosen Namen von duckdns.org verwenden: in `deploy/.env` bei `HB_DOMAIN` eintragen und `./deploy/setup.sh` erneut starten |
| App: „The relay does not know this iPhone“ | Das iPhone ist mit einem anderen Server gekoppelt. In der App **Unpair this iPhone** wählen und den QR-Code (`./deploy/hb pairing`) neu scannen |
| ChatGPT zeigt keinen Entwicklermodus | Das Konto braucht Plus oder höher. Bei Firmenkonten kann ihn ein Admin gesperrt haben |
| ChatGPT/Claude: „Verbindung fehlgeschlagen“ | Prüfen, ob die Connector-Adresse **vollständig** kopiert ist (`./deploy/hb connector-url`) und `./deploy/hb status` funktioniert. Danach die Protokolle ansehen (siehe Schritt 7) |
| Antworten enthalten alte Daten | In der App auf **Sync now** tippen und auf dem Server `./deploy/hb fetch` ausführen |
| `another healthbridge command is using this state directory` | Gerade läuft der automatische Abruf. Ein paar Sekunden warten und den Befehl wiederholen |

## Wie sicher ist das?

- Zwischen iPhone und Server sind die Daten **Ende-zu-Ende-verschlüsselt**. Das Relay
  speichert nur unlesbare Pakete, bis der Agent sie abholt.
- Der **Agent** auf dem Server entschlüsselt die Daten und speichert sie dort. Wer den
  Server kontrolliert, kann sie lesen.
- Die **Connector-Adresse** enthält ein langes Zufallsgeheimnis (256 Bit). Ohne sie gibt
  der Server nur „nicht gefunden“ zurück. Gib sie an niemanden weiter. Bei Verdacht
  erzeugst du mit `./deploy/hb connector-url --rotate` sofort eine neue.
- Über die Connector-Adresse kann man Daten nur **lesen**. Die Upload-Adresse hat ein
  eigenes Geheimnis und erlaubt nur, Trainings hinzuzufügen, nicht sie zu lesen, zu
  ändern oder zu löschen.
- Garmin-Originaldateien enthalten die GPS-Strecke. Sie bleiben auf dem Server;
  ChatGPT und Claude bekommen nie GPS-Positionen.
- Neue Konten kann auf deinem Relay nur anlegen, wer das Relay-Token aus
  `deploy/.env` kennt.
