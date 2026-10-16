# Garmin-Trainings auswerten (z. B. fenix 8)

Apple Health bekommt von Garmin nur einen Teil der Daten, etwa die Workout-Zusammenfassung,
Puls und Schlaf. Runden, Leistung, Laufdynamik, Training Effect, Kraftsätze und
Hyrox-Abschnitte fehlen dort. HealthBridge liest deshalb zusätzlich die **Originaldateien
deiner Uhr** (FIT-Dateien) und stellt sie ChatGPT, Claude und Claude Code zur Verfügung.

Diese Anleitung setzt voraus, dass HealthBridge schon läuft: auf dem Server
([`SERVER_START.md`](SERVER_START.md)) oder auf dem Mac ([`MAC_START.md`](MAC_START.md)).

## Was ausgewertet wird

| Sport | Auswertung |
| --- | --- |
| Alle | Dauer, Distanz, Puls, Pulszonen, Training Effect (aerob/anaerob), Trainingsbelastung der Uhr, TRIMP, Atemfrequenz, Temperatur, gefühlte Anstrengung, Runden |
| Laufen | Pace pro Runde, **Bestzeiten** (400 m bis Marathon, auch mitten im Lauf), Laufdynamik (Bodenkontaktzeit, Balance, vertikale Bewegung, Schrittlänge), Lauf-Leistung, **aerobe Entkopplung**, Effizienzfaktor |
| Rad | Leistung, Normalized Power, IF, TSS, **beste Leistung über 5 s bis 60 min**, Pedal-Balance, Entkopplung, Effizienzfaktor |
| Kraft | jede Übung mit Sätzen, Wiederholungen, Gewicht, Volumen, **geschätztem Maximalgewicht (1RM)** und Pausen |
| HYROX | jeder Lauf und jede Station einzeln, Laufzeit gegen Stationszeit, **wie stark die Läufe nachlassen**, langsamste Station |
| Über Wochen | Umfang pro Sportart, **akute und chronische Belastung** samt Verhältnis, Fitness/Ermüdung/Form, Intensitätsverteilung, persönliche Rekorde |

Die Werte stehen ChatGPT und Claude zusammen mit deinen Apple-Health-Daten zur Verfügung.
Du kannst also auch fragen: *„Wie hängt mein Schlaf mit meiner Trainingsbelastung
zusammen?“*

> **Warum die Dateien nicht automatisch kommen:** Garmin bietet für private Projekte
> keine Schnittstelle an. Das Entwicklerprogramm ist nur für Firmen und nimmt seit
> September 2026 keine neuen Bewerber an. Der Umweg über Strava fällt weg, weil Strava die
> Auswertung mit KI verbietet. Deine eigenen Dateien darfst du aber jederzeit
> herunterladen. Sie enthalten mehr als jede Schnittstelle.

> **Datenschutz:** Die Originaldateien werden auf deinem Server bzw. Mac gespeichert,
> **mit GPS-Strecke**. ChatGPT und Claude bekommen **nie** GPS-Positionen zu sehen, nur
> Zeiten, Strecken, Puls, Leistung und ähnliche Werte.

---

## Schritt 1: Die Upload-Seite öffnen (nur Server)

Auf dem Mac ohne Server springst du zu [„Mit dem Mac statt Server“](#mit-dem-mac-statt-server).

Am Ende von `setup.sh` steht unter Punkt 3 die **Upload-Adresse**, zum Beispiel
`https://203-0-113-5.sslip.io/upload/ZR9Z…`. Wenn du sie nicht mehr hast:

1. Mit dem Server verbinden: `ssh root@<IP-Adresse>`
2. Eingeben:
   ```bash
   cd Healthsync-copy && ./deploy/hb connector-url --upload
   ```
3. Die angezeigte Adresse im Browser öffnen, am Computer und am besten auch auf dem
   iPhone in Safari. Dort ein **Lesezeichen** anlegen.

Die Upload-Adresse hat ein **eigenes Geheimnis**. Wer sie kennt, kann nur Trainings
hinzufügen, aber nichts lesen. Ein Lesezeichen im Browser verrät also nicht deine Daten.
Gib sie trotzdem nicht weiter. Eine neue Adresse bekommst du mit
`./deploy/hb connector-url --upload --rotate`; die alte gilt dann nicht mehr.

> Hast du HealthBridge vor diesem Update eingerichtet? Dann einmal auf dem Server
> `cd Healthsync-copy && git pull && ./deploy/setup.sh` ausführen. Deine Daten bleiben
> erhalten.

## Schritt 2: Trainings holen. Drei Wege

| Weg | Wann | Aufwand |
| --- | --- | --- |
| **A: einzelnes Training aus Garmin Connect** | nach jedem Training | ca. 30 Sekunden |
| **B: direkt von der Uhr per USB** | einmal pro Woche alle neuen auf einmal | ca. 2 Minuten |
| **C: kompletter Konto-Export** | einmalig, für die ganze Vergangenheit | Anfrage, dann Stunden bis Tage warten |

Doppelte Trainings erkennt HealthBridge automatisch. Lädst du dieselbe Datei zweimal
hoch oder kombinierst du die Wege, entsteht nichts doppelt.

### Weg A: Einzelnes Training aus Garmin Connect

**Am Computer**

1. Im Browser [connect.garmin.com](https://connect.garmin.com) öffnen und anmelden.
2. Links im Menü **Aktivitäten → Alle Aktivitäten** wählen.
3. Das Training anklicken.
4. Oben rechts auf das **Zahnrad ⚙** klicken und **„Original exportieren“** wählen
   (englisch *Export Original*). Es wird eine **ZIP-Datei** heruntergeladen, zum Beispiel
   `12345678901.zip`. **Nicht entpacken.**
5. Die Upload-Seite öffnen, auf **„Choose files“** klicken, die ZIP-Datei auswählen und
   auf **„Upload“** klicken. Nach ein paar Sekunden erscheint „1 new activities“.

Nimm wirklich „Original exportieren“ und nicht „Als GPX/TCX exportieren“. Nur die
Originaldatei enthält alle Werte.

**Auf dem iPhone**

In der Garmin-Connect-App gibt es den Original-Export nicht. Er geht aber in Safari:

1. In Safari [connect.garmin.com](https://connect.garmin.com) öffnen und anmelden.
2. Links in der Adressleiste auf **„aA“** tippen und **„Desktop-Website anfordern“** wählen.
3. Weiter wie am Computer ab Punkt 2. Die ZIP-Datei landet in der App **Dateien** im Ordner
   **Downloads**.
4. Die Upload-Seite öffnen (Lesezeichen), auf **„Choose files“** tippen, dann
   **„Durchsuchen“** → **Downloads** → ZIP-Datei auswählen und **„Upload“** tippen.

Du kannst auch mehrere ZIP-Dateien auf einmal auswählen.

### Weg B: Direkt von der Uhr (USB)

1. Die fenix 8 mit dem Ladekabel an den Computer anschließen.
2. **Windows:** Die Uhr erscheint im Explorer unter „Dieser PC“ als „fenix 8“.
   **Mac:** macOS zeigt Garmin-Uhren nicht von selbst an. Installiere das kostenlose
   Programm [OpenMTP](https://openmtp.ganeshrvel.com) und schließe vorher **Garmin
   Express** vollständig, auch das Symbol in der Menüleiste. Ist „Android File Transfer“
   installiert, kann es OpenMTP blockieren. Dann deinstallieren.
3. Den Ordner **GARMIN → Activity** öffnen. Jede `.fit`-Datei ist ein Training.
4. Alle Dateien markieren und auf die Upload-Seite ziehen (oder mit „Choose files“
   auswählen) und **„Upload“** klicken. Bereits bekannte Trainings werden übersprungen.

Ist der Speicher voll, löscht die Uhr alte Trainings. Für die ganze Vergangenheit ist
deshalb Weg C besser.

### Weg C: Die gesamte Vergangenheit (Konto-Export)

1. Im Browser bei Garmin anmelden und die Seite **Datenverwaltung** öffnen:
   [garmin.com/account/datamanagement](https://www.garmin.com/account/datamanagement/).
   Dort **„Daten exportieren“** (*Export Your Data*) und **„Export anfordern“** wählen.
   Die Bezeichnungen ändert Garmin gelegentlich. Notfalls über „Konto → Datenverwaltung“
   suchen.
2. Garmin schickt eine **E-Mail mit Download-Link**. Meist dauert das einen bis zwei Tage,
   manchmal länger. Der Link läuft nach einiger Zeit ab, also bald herunterladen.
3. Die ZIP-Datei herunterladen. Sie kann mehrere Gigabyte groß sein. **Nicht entpacken.**
4. Die ZIP-Datei auf der Upload-Seite hochladen. Das dauert je nach Größe und Internet
   einige Minuten. Bitte die Seite offen lassen, bis das Ergebnis erscheint. Solange der
   Import läuft, antworten ChatGPT und Claude nicht auf Fragen an HealthBridge; danach
   wieder normal. Der Export enthält auch Dateien ohne Training (Schlaf, Einstellungen …).
   Sie werden übersprungen und als „files without a workout skipped“ gezählt.

**Größer als 4 GB?** Dann so:

- Die ZIP-Datei entpacken und die einzelnen Teile hochladen. Die Trainings stecken in
  ZIP-Dateien namens `UploadedFiles_…zip` in einem Unterordner von `DI_CONNECT`.
- Oder die Datei direkt auf den Server kopieren. Am Computer im Terminal bzw. in der
  PowerShell:
  ```bash
  scp garmin-export.zip root@<IP-Adresse>:Healthsync-copy/deploy/import/
  ```
  Danach auf dem Server: `cd Healthsync-copy && ./deploy/hb import /import`

## Schritt 3: Fragen stellen

In ChatGPT und Claude erscheinen vier neue Werkzeuge: `training_activities`,
`training_activity`, `training_load` und `training_records`.

**ChatGPT** merkt sich die Werkzeugliste eines Connectors. Falls die neuen Werkzeuge
fehlen: **Einstellungen → Apps & Connectors → HealthBridge** öffnen und **„Aktualisieren“**
(*Refresh*) wählen. Hilft das nicht, den Connector löschen und wie in
[`SERVER_START.md`](SERVER_START.md) Schritt 6a neu anlegen. **Claude** lädt die Liste
bei jedem neuen Chat neu.

**Gute erste Fragen**

- *„Analysiere meinen letzten Lauf: Runden, Pulszonen und Entkopplung.“*
- *„Wie haben sich meine Bestzeiten über 5 und 10 km im letzten Jahr entwickelt?“*
- *„Was sind meine besten 20-Minuten-Watt auf dem Rad, und welche FTP ergibt das ungefähr?“*
- *„Wie hat sich mein geschätztes Maximalgewicht bei Kniebeugen und Bankdrücken entwickelt?“*
- *„Analysiere meinen letzten HYROX: Wo verliere ich die meiste Zeit, und wie stark
  bauen meine Läufe ab?“*
- *„Wie ist meine Trainingsbelastung der letzten 12 Wochen? Steigere ich zu schnell?“*
- *„Trainiere ich nach dem 80/20-Prinzip (80 % locker, 20 % hart)?“*
- *„Vergleiche meinen Ruhepuls und meine HRV aus Apple Health mit meiner Belastung.“*

## Tipps für die fenix 8

**HYROX.** HealthBridge erkennt ein HYROX-Training am **Namen der Aktivität**: Er muss
„HYROX“ enthalten (Groß-/Kleinschreibung egal). So bekommst du saubere Abschnitte:

- Lege auf der Uhr eine eigene Aktivität mit dem Namen **HYROX** an, zum Beispiel als
  Kopie von „Laufband“ oder als Multisport-Aktivität mit abwechselnd Laufen und Station.
- Bei einer einfachen Aktivität nach **jedem Lauf und jeder Station die Runden-Taste**
  drücken. Das ergibt 16 Runden. Als Lauf zählt eine Runde ab 300 m, die schneller als
  etwa 10:25 /km ist, alles andere als Station.
- Hat die Uhr keine Stationsnamen gespeichert und gibt es genau 8 Stationen, verwendet
  HealthBridge die offizielle Reihenfolge (SkiErg, Sled Push, …) und markiert die Namen
  mit \*.

Den Namen später in Garmin Connect zu ändern hilft nicht, denn die Originaldatei behält
den Namen von der Uhr.

**Krafttraining.** Die fenix 8 zählt Wiederholungen und erkennt Übungen. Trage das
**Gewicht direkt auf der Uhr** nach jedem Satz ein. Korrekturen, die du später in Garmin
Connect machst, stehen nicht in der Originaldatei. Ohne Gewicht zählt eine Übung als
Körpergewicht (kein Volumen, kein 1RM).

**Laufdynamik.** Bodenkontaktzeit, Balance und vertikale Bewegung misst die Uhr nur mit
einem Brustgurt mit Laufdynamik (z. B. HRM-Pro, HRM 600) oder einem Running Dynamics Pod.

**HRV während des Trainings.** Optional: In den Einstellungen der Uhr die Option
**„HRV aufzeichnen“** (*Log HRV*) einschalten. Bei vielen Garmin-Uhren steht sie unter
„Physiologische Messwerte“, je nach Software-Version auch woanders; notfalls in den
Einstellungen nach „HRV“ suchen. Die Werte sind mit Brustgurt deutlich genauer. Garmin
Connect zeigt sie nicht an, HealthBridge schon.

## Mit dem Mac statt Server

Ohne Server importierst du im Terminal:

```bash
healthbridge import ~/Downloads/12345678901.zip      # eine Datei
healthbridge import ~/Downloads/garmin-export.zip    # Konto-Export
healthbridge import /Volumes/…/GARMIN/Activity        # ein Ordner
```

Claude Code sieht die Trainings danach sofort. Im Terminal geht es auch direkt:

| Befehl | Zeigt |
| --- | --- |
| `healthbridge activities --days 30` | alle Trainings der letzten 30 Tage |
| `healthbridge activity latest --kind run` | den letzten Lauf im Detail (`--kind strength`, `hyrox`, `bike` …) |
| `healthbridge training --days 84` | Umfang und Belastung der letzten 12 Wochen |
| `healthbridge records` | persönliche Rekorde des letzten Jahres |
| `healthbridge delete-activity ID` | ein Training wieder löschen (ID aus `activities`) |

Auf dem Server funktionieren dieselben Befehle mit `./deploy/hb` davor, zum Beispiel
`./deploy/hb activities`.

## Ausprobieren ohne Uhr

Drei erfundene Beispieltrainings (Lauf, Kraft, HYROX) erzeugen und importieren:

```bash
healthbridge dev sample-fit --out ~/hb-beispiele && healthbridge import ~/hb-beispiele
# auf dem Server:
./deploy/hb dev sample-fit --out /data/beispiele && ./deploy/hb import /data/beispiele
```

Danach wieder entfernen, damit sie deine Rekorde nicht verfälschen:
`healthbridge delete-activity --samples` (Server: `./deploy/hb delete-activity --samples`).

## Begriffe kurz erklärt

| Begriff | Bedeutung |
| --- | --- |
| Training Effect | Garmins Einschätzung des Trainingsreizes von 0 bis 5. Ab 3 verbessernd, 5 überlastend |
| Trainingsbelastung | Garmins Belastungswert pro Training (aus dem Sauerstoff-Nachverbrauch geschätzt) |
| Akut / chronisch / Verhältnis | Belastung der letzten 7 Tage gegen den Wochenschnitt der letzten 4 Wochen. 0,8–1,3 gilt als ausgewogen, über 1,5 als sprunghafte Steigerung |
| Fitness / Ermüdung / Form | Langfristige (42 Tage) und kurzfristige (7 Tage) Belastung. Form = Fitness − Ermüdung, negativ heißt müde |
| Aerobe Entkopplung | Wie stark der Puls bei gleichem Tempo oder gleicher Leistung in der zweiten Hälfte steigt. Unter 5 % spricht für gute Grundlagenausdauer. Nur bei gleichmäßigen Einheiten sinnvoll |
| Effizienzfaktor (EF) | Tempo bzw. Leistung pro Herzschlag. Steigt er bei ähnlicher Anstrengung, wirst du effizienter |
| TRIMP | Belastung aus Dauer und Pulshöhe. Ersatz, wenn die Uhr keinen eigenen Wert liefert |
| 1RM (Epley) | Geschätztes Maximalgewicht für eine Wiederholung, aus Sätzen mit 1–12 Wiederholungen |
| NP / IF / TSS | Normalized Power, Intensitätsfaktor (NP ÷ FTP) und Training Stress Score beim Radfahren |

Die Auswertungen sind Trainingshinweise, keine medizinische Beratung.

## Wenn etwas hakt

| Problem | Lösung |
| --- | --- |
| „not a FIT file, .fit.gz or ZIP archive“ | Es wurde GPX/TCX oder eine andere Datei gewählt. In Garmin Connect „Original exportieren“ nehmen |
| „0 new activities, … already known“ | Die Trainings waren schon importiert. Alles in Ordnung |
| „files without a workout skipped“ | Schlaf-, Einstellungs- oder Monitoring-Dateien. Normal beim Konto-Export und per USB |
| HYROX wird als Lauf angezeigt | Der Aktivitätsname auf der Uhr enthält nicht „HYROX“ (siehe Tipps) |
| Übungsnamen oder Gewichte fehlen | Auf der Uhr eintragen. Korrekturen in Garmin Connect stehen nicht in der Originaldatei |
| ChatGPT kennt die neuen Werkzeuge nicht | Connector aktualisieren (siehe Schritt 3) |
| Upload bricht bei großen Dateien ab | Datei in Teilen hochladen oder per `scp` kopieren (Weg C) |
| Upload-Seite zeigt „404 page not found“ | Adresse unvollständig kopiert oder mit `--upload --rotate` erneuert. Neu anzeigen mit `./deploy/hb connector-url --upload` |
| Hinweis „damaged or incomplete“ bei einem Training | Die Datei wurde unvollständig kopiert. Ausgewertet wird, was lesbar ist. Besser die Datei aus Garmin Connect („Original exportieren“) noch einmal hochladen |
| Uhrzeit wirkt verschoben | Die Zeiten folgen der Ortszeit der Uhr beim Training |
