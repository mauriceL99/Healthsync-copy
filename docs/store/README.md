# Store-Unterlagen (vorbereitet, nicht in der App)

HealthBridge ist zunächst für die **Eigennutzung** gedacht (persönliche Edition, siehe
[`../MAC_START.md`](../MAC_START.md)). Dafür wird nichts aus diesem Ordner gebraucht.
Die Unterlagen liegen bereit, falls die App später per TestFlight an Freunde oder in den
App Store geht.

| Datei | Inhalt |
| --- | --- |
| [`datenschutz.md`](datenschutz.md) | Datenschutzerklärung (Deutsch, Entwurf) |
| [`privacy-policy.md`](privacy-policy.md) | Privacy Policy (Englisch, Entwurf) |
| [`app-privacy.md`](app-privacy.md) | Antworten für „App-Datenschutz“ in App Store Connect |
| [`listing.md`](listing.md) | Name, Untertitel, Beschreibung, Schlüsselwörter, Screenshots, Prüfnotizen, TestFlight |

## Checkliste, falls es ernst wird

1. Apple Developer Program (99 €/Jahr).
2. Für den App Store: Store-Edition aktivieren ([`../../ios/README.md`](../../ios/README.md),
   „Store-Edition“). Für TestFlight unter Freunden geht auch die persönliche Edition.
3. App in App Store Connect anlegen (Bundle-ID wie in `ios/Configuration/Local.xcconfig`).
4. Platzhalter in den Datenschutztexten ausfüllen, veröffentlichen (z. B. GitHub Pages)
   und prüfen lassen.
5. Verschlüsselungs-Exportfrage klären (siehe `app-privacy.md`).
6. Build in Xcode: Product → Archive → Distribute App → App Store Connect.
7. Screenshots, Texte und Prüfnotizen aus `listing.md` eintragen.
8. Offene Produktfragen aus `.slop-audit.md` entscheiden (z. B. „Manage subscription“
   bei Lifetime-Käufern, Billing Grace Period).
