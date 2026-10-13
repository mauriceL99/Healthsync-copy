# App-Privacy-Angaben (App Store Connect) – Entwurf

App Store Connect fragt unter **App-Datenschutz**, welche Daten „erfasst“ werden. Apple
zählt Daten als erfasst, wenn sie das Gerät so verlassen, dass der Entwickler oder seine
Partner länger darauf zugreifen können, als für die Echtzeit-Bearbeitung der Anfrage
nötig ist. Ob das zutrifft, hängt davon ab, **wer das Relay betreibt**. Vor dem Einreichen
mit der aktuellen Apple-Doku („App privacy details on the App Store“) abgleichen.

## Fall A: Nutzer betreiben ihr Relay selbst (wie bei der persönlichen Edition)

Der Herausgeber erhält keine Daten. Die App sendet nur an ein Relay, das der Nutzer
über seinen eigenen Agenten festlegt.

* Antwort: **„Nein, wir erfassen keine Daten von dieser App.“**
* Begründung für Rückfragen: Ende-zu-Ende-verschlüsselt an den eigenen Agenten des
  Nutzers, kein vom Herausgeber betriebener Server, keine SDKs.

## Fall B: Der Herausgeber betreibt ein Relay für Nutzer

Das Relay speichert Ciphertext, Gerätemetadaten und Zeitpunkte. Vorschlag, eher
vollständig als knapp:

| Kategorie (App Store Connect) | Datentyp | Zweck | Mit Identität verknüpft | Tracking |
| --- | --- | --- | --- | --- |
| Gesundheit & Fitness | Gesundheit; Fitness | App-Funktionalität | Nein | Nein |
| Identifikatoren | Geräte-ID (zufällige Relay-Geräte-ID) | App-Funktionalität | Nein | Nein |
| Diagnose | Sonstige Diagnosedaten (App-Version, Plattform) | App-Funktionalität | Nein | Nein |

Anmerkungen:
* Die Gesundheitsdaten sind für den Herausgeber unlesbar (Ende-zu-Ende-verschlüsselt).
  Ob sie trotzdem anzugeben sind, lässt Apples Definition offen. Mit der Angabe ist man
  auf der sicheren Seite.
* Der Gerätename stammt aus den iOS-Einstellungen und kann einen Personennamen enthalten
  (z. B. „iPhone von Maurice“). Entweder zusätzlich „Kontaktinformationen → Name“
  angeben oder die App so ändern, dass sie für ein fremdes Relay einen neutralen Namen
  sendet.
* Kein Datentyp wird für Tracking, Werbung oder Profilbildung verwendet.

## Weitere Angaben

| Feld | Wert |
| --- | --- |
| Datenschutzrichtlinien-URL | Pflicht (auch für externe TestFlight-Tester). Inhalt: `datenschutz.md` / `privacy-policy.md`, z. B. als GitHub Page |
| HealthKit | Nutzung in der App-Beschreibung erwähnen; Health-Daten nicht in iCloud, nicht für Werbung (App Review Guidelines 5.1.3) |
| Verschlüsselung (Exportkontrolle) | Die App verschlüsselt mit CryptoKit (Standardverfahren). App Store Connect fragt bei jedem Build danach, solange `ITSAppUsesNonExemptEncryption` nicht in `Info.plist` steht. Die richtige Antwort vor dem ersten Upload anhand von Apples „Complying with Encryption Export Regulations“ klären |
