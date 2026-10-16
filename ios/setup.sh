#!/bin/bash
# Einmalige Einrichtung auf dem Mac, um HealthBridge auf das eigene iPhone zu bringen.
#
#   cd ios && ./setup.sh
#
# Fragt Apple-Team-ID und Bundle-ID-Präfix ab, speichert beides in
# Configuration/Local.xcconfig (nicht eingecheckt), erzeugt HealthBridge.xcodeproj mit
# XcodeGen und öffnet das Projekt in Xcode. Darf beliebig oft erneut laufen.
set -euo pipefail
cd "$(dirname "$0")"

local_config="Configuration/Local.xcconfig"

fail() {
    echo "✗ $*" >&2
    exit 1
}

# --- Xcode ---------------------------------------------------------------------------
developer_dir=$(xcode-select -p 2>/dev/null || true)
if [[ -z "$developer_dir" || "$developer_dir" == *CommandLineTools* ]]; then
    if [[ -d /Applications/Xcode.app ]]; then
        fail "Xcode ist installiert, aber nicht ausgewählt. Bitte ausführen:
    sudo xcode-select -s /Applications/Xcode.app && sudo xcodebuild -runFirstLaunch
und danach ./setup.sh erneut starten."
    fi
    fail "Xcode fehlt. Aus dem App Store installieren, einmal starten, dann ./setup.sh erneut ausführen."
fi
echo "✓ $(xcodebuild -version | head -1)"

# --- XcodeGen ------------------------------------------------------------------------
if ! command -v xcodegen >/dev/null 2>&1; then
    command -v brew >/dev/null 2>&1 || fail "Homebrew fehlt (https://brew.sh). Installieren, dann ./setup.sh erneut ausführen."
    echo "Installiere XcodeGen über Homebrew…"
    brew install xcodegen
fi
echo "✓ XcodeGen $(xcodegen --version 2>/dev/null | tail -1)"

# --- Team and bundle ID --------------------------------------------------------------
# Die Team-ID steht als OU im Zertifikat „Apple Development“ im Anmelde-Schlüsselbund.
# Xcode legt es an unter Settings → Accounts → Manage Certificates… → + .
detect_team() {
    security find-certificate -a -c "Apple Development" -p 2>/dev/null \
        | openssl x509 -noout -subject 2>/dev/null \
        | sed -n 's/.*OU *= *\([A-Z0-9]\{10\}\).*/\1/p' \
        | head -n 1
}

read_value() { # file key
    { sed -n "s/^$2 *= *//p" "$1" 2>/dev/null || true; } | head -n 1
}

team=""
prefix=""
if [[ -f "$local_config" ]]; then
    team=$(read_value "$local_config" DEVELOPMENT_TEAM)
    prefix=$(read_value "$local_config" HB_BUNDLE_ID_PREFIX)
    echo "Aktuell: Team '${team:-keins}', Bundle-ID ${prefix:-?}.healthbridge"
    read -r -p "Beibehalten? [J/n] " keep
    if [[ "$keep" == [nN]* ]]; then
        team=""
        prefix=""
    fi
fi

if [[ -z "$prefix" ]]; then
    detected=$(detect_team || true)
    if [[ -n "$detected" ]]; then
        read -r -p "Apple-Team-ID [$detected]: " team
        team=${team:-$detected}
    else
        echo "Kein Zertifikat „Apple Development“ gefunden. In Xcode: Settings → Accounts → Apple-ID"
        echo "hinzufügen → Manage Certificates… → + → Apple Development. Danach ./setup.sh erneut"
        echo "starten – oder jetzt die Team-ID eingeben (leer lassen: Team in Xcode auswählen)."
        read -r -p "Apple-Team-ID: " team
    fi
    if [[ -n "$team" && ! "$team" =~ ^[A-Z0-9]{10}$ ]]; then
        fail "Eine Team-ID hat genau 10 Zeichen (A–Z, 0–9), eingegeben: '$team'."
    fi

    user_part=$(id -un | tr '[:upper:]' '[:lower:]' | tr -cd 'a-z0-9-')
    default_prefix="de.${user_part:-me}"
    read -r -p "Bundle-ID-Präfix (die App heißt dann <präfix>.healthbridge) [$default_prefix]: " prefix
    prefix=${prefix:-$default_prefix}
    if [[ ! "$prefix" =~ ^[A-Za-z0-9-]+(\.[A-Za-z0-9-]+)+$ ]]; then
        fail "Bitte eine umgekehrte Domain wie de.maurice angeben, eingegeben: '$prefix'."
    fi

    cat >"$local_config" <<EOF
// Von setup.sh geschrieben – persönliche Signierwerte, nicht eingecheckt.
DEVELOPMENT_TEAM = $team
HB_BUNDLE_ID_PREFIX = $prefix
EOF
    echo "✓ Gespeichert: $local_config"
fi

# --- Project -------------------------------------------------------------------------
xcodegen generate
echo
echo "Nächste Schritte:"
echo "  1. iPhone per Kabel verbinden und entsperren („Diesem Computer vertrauen“)."
echo "  2. In Xcode oben das iPhone als Ziel wählen und ⌘R drücken."
if [[ -z "$team" ]]; then
    echo "     Kein Team gesetzt: unter HealthBridge → Signing & Capabilities → Team auswählen."
fi
echo "  3. Nur beim ersten Mal: auf dem iPhone den Entwicklermodus einschalten (Einstellungen →"
echo "     Datenschutz & Sicherheit → Entwicklermodus) und mit kostenloser Apple-ID dem"
echo "     Entwickler vertrauen (Einstellungen → Allgemein → VPN und Geräteverwaltung)."
echo "  Ausführlich: docs/MAC_START.md"
open HealthBridge.xcodeproj
