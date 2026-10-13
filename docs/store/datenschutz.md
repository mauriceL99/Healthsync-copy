# Datenschutzerklärung – HealthBridge (Entwurf)

> **Entwurf, keine Rechtsberatung.** Platzhalter in `[eckigen Klammern]` ersetzen. Vor
> einer Veröffentlichung (TestFlight für Externe, App Store) prüfen lassen. Die Aussagen
> beschreiben den Stand des Codes in diesem Repository. Ändert sich der Code, muss der
> Text mitgezogen werden.

Stand: [Datum]

## 1. Kurzfassung

* HealthBridge liest die Apple-Health-Daten, die du auswählst, **verschlüsselt sie auf
  deinem iPhone** und schickt sie an den KI-Agenten, mit dem du die App gekoppelt hast.
* Der Herausgeber der App erhält **keine** deiner Daten. Es gibt keine Werbung, kein
  Tracking und keine Analyse- oder Werbe-SDKs.
* Die Übertragung läuft über einen Relay-Server, den du (bzw. dein Agent) festlegst. Das
  Relay sieht nur verschlüsselte Daten und wenige Metadaten (siehe Abschnitt 4).

## 2. Verantwortlicher

[Vorname Nachname]
[Anschrift]
E-Mail: [E-Mail-Adresse]

Für das Relay ist verantwortlich, wer es betreibt. Betreibst du es selbst (Standard bei
HealthBridge), bist du das selbst. Betreibt der Herausgeber ein Relay für dich, gilt
zusätzlich Abschnitt 5.

## 3. Verarbeitung auf deinem iPhone

| Daten | Zweck | Speicherort |
| --- | --- | --- |
| Apple-Health-Daten der von dir gewählten Kategorien (z. B. Aktivität, Herz, Schlaf, Körper, Vitalwerte, Ernährung, Achtsamkeit, Workouts) | Übertragung an deinen Agenten | werden nur gelesen, nicht dauerhaft in der App gespeichert |
| Kopplungsdaten (öffentliche Schlüssel des Agenten, Relay-Adresse, Geräte-ID) und der private Geräteschlüssel | Verschlüsselung und Anmeldung beim Relay | iOS-Schlüsselbund, an dieses Gerät gebunden (wird nicht auf andere Geräte übertragen) |
| Einstellungen und Sync-Status (Zeitpunkte, gewählte Kategorien) | Funktion der App | App-Einstellungen auf dem Gerät |
| Aktivitätsprotokoll (Zeitpunkt, Anzahl und Größe der Uploads, Fehlermeldungen; **keine Gesundheitswerte**) | Anzeige in der App | Datei in der App, geschützt bis zur ersten Entsperrung |

Die Kamera wird nur zum Scannen des Kopplungs-QR-Codes verwendet. Es werden keine Bilder
gespeichert.

Gesundheitsdaten aus Apple Health werden nicht in iCloud gespeichert, nicht für Werbung
verwendet und nicht an Dritte verkauft oder weitergegeben.

## 4. Übertragung an deinen Agenten über ein Relay

Die App verschlüsselt jede Übertragung auf dem iPhone für den öffentlichen Schlüssel
deines Agenten (X25519, HKDF-SHA256, ChaCha20-Poly1305). Nur dein Agent kann sie
entschlüsseln. Das Relay speichert:

| Daten | Wie lange |
| --- | --- |
| verschlüsselte Datenpakete (Inhalt für das Relay unlesbar) | bis dein Agent sie abholt, höchstens 30 Tage |
| Größe und Zeitpunkt der Pakete | wie die Pakete |
| Geräteeintrag: zufällige Geräte-ID, öffentlicher Geräteschlüssel, Gerätename (wie in den iOS-Einstellungen), Plattform, App-Version, Kopplungs- und letzter Zugriffszeitpunkt | bis der Agent sein Relay-Konto löscht (`healthbridge unlink --delete-relay-account`) |
| IP-Adresse | nur kurzzeitig im Arbeitsspeicher zur Begrenzung der Anfragerate, nicht in Protokollen |

Nach dem Entschlüsseln liegen die Daten bei deinem Agenten, also auf dem Rechner, auf
dem du die HealthBridge-CLI eingerichtet hast. Was der KI-Agent (z. B. Claude Code) damit
tut, regeln dessen Anbieter und deine Einstellungen dort.

## 5. Relay des Herausgebers (nur falls angeboten)

[Nur ausfüllen, wenn der Herausgeber ein Relay für Nutzer betreibt: Hoster und Standort,
Auftragsverarbeitungsvertrag, Speicherdauer wie in Abschnitt 4, Kontakt für
Löschanfragen.]

## 6. Rechtsgrundlagen

* Gesundheitsdaten: deine ausdrückliche Einwilligung (Art. 9 Abs. 2 lit. a, Art. 6
  Abs. 1 lit. a DSGVO). Du erteilst sie über die Health-Freigabe in iOS und durch das
  Koppeln mit deinem Agenten.
* Betrieb der Kopplung und Übertragung: Erfüllung der Nutzung der App (Art. 6 Abs. 1
  lit. b DSGVO).

Du kannst die Einwilligung jederzeit widerrufen: In iOS unter Einstellungen → Health →
Datenzugriff & Geräte → HealthBridge die Freigaben entziehen oder in der App **Unpair
this iPhone** wählen.

## 7. Käufe (nur Store-Edition)

Falls die App ein kostenpflichtiges „Pro“-Angebot enthält, wickelt Apple den Kauf ab.
Der Herausgeber erhält dabei keine Zahlungsdaten. Die App prüft nur auf dem Gerät, ob
ein gültiger Kauf vorliegt.

## 8. Löschen

* **In der App:** „Unpair this iPhone“ meldet das Gerät beim Relay ab und löscht alle
  Schlüssel und Kopplungsdaten aus dem Schlüsselbund. Das Löschen der App entfernt
  Einstellungen und Protokoll.
* **Beim Relay:** `healthbridge unlink --delete-relay-account` löscht Konto, Gerät und
  nicht abgeholte Pakete.
* **Beim Agenten:** Die entschlüsselten Daten liegen im Ordner `~/.healthbridge` auf
  dem Rechner des Agenten und lassen sich dort löschen.

## 9. Deine Rechte

Du hast das Recht auf Auskunft, Berichtigung, Löschung, Einschränkung der Verarbeitung,
Datenübertragbarkeit und Widerspruch sowie auf Beschwerde bei einer
Datenschutz-Aufsichtsbehörde. Da der Herausgeber keine deiner Daten erhält, richten sich
diese Rechte in der Regel an den Betreiber des Relays. Für Fragen erreichst du den
Herausgeber unter [E-Mail-Adresse].

## 10. Kein Medizinprodukt

HealthBridge ist kein Medizinprodukt, stellt keine Diagnosen und ersetzt keine ärztliche
Beratung.
