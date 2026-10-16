#!/bin/bash
# Richtet HealthBridge auf einem frischen Linux-Server (Ubuntu oder Debian) ein:
# Relay + Agent mit automatischem HTTPS. Aus dem geklonten Repository starten:
#
#   sudo ./deploy/setup.sh
#
# Darf jederzeit erneut laufen (z. B. nach `git pull`): Schlüssel, Daten und
# Einstellungen bleiben erhalten, die Programme werden neu gebaut.
set -euo pipefail
cd "$(dirname "$0")"

env_file=".env"

say() { printf '\n\033[1m%s\033[0m\n' "$*"; }
fail() {
    printf '\n\033[31m✗ %s\033[0m\n' "$*" >&2
    exit 1
}
compose() { docker compose -f docker-compose.yml "$@"; }

[[ $EUID -eq 0 ]] || fail "Bitte mit sudo starten: sudo ./deploy/setup.sh"

# --- Docker ----------------------------------------------------------------------
if ! command -v docker >/dev/null 2>&1; then
    say "Installiere Docker (dauert 1–2 Minuten)…"
    curl -fsSL https://get.docker.com | sh
fi
docker compose version >/dev/null 2>&1 || fail "Docker Compose fehlt. Bitte Docker neu installieren: curl -fsSL https://get.docker.com | sh"

# --- Einstellungen ---------------------------------------------------------------
public_ip() {
    curl -4 -fsS --max-time 10 https://api.ipify.org 2>/dev/null ||
        curl -4 -fsS --max-time 10 https://ifconfig.me 2>/dev/null || true
}

random_hex() { head -c 16 /dev/urandom | od -An -tx1 | tr -d ' \n'; }

if [[ -f $env_file ]]; then
    # shellcheck disable=SC1090
    source "$env_file"
    say "Vorhandene Einstellungen: https://${HB_DOMAIN}"
else
    ip=$(public_ip)
    suggestion=""
    [[ -n $ip ]] && suggestion="${ip//./-}.sslip.io"
    say "Unter welcher Adresse soll der Server erreichbar sein?"
    echo "Ohne eigene Domain einfach Enter drücken: ${suggestion:-(keine IP gefunden, bitte Domain eingeben)}"
    echo "(sslip.io ist ein kostenloser Dienst, der diesen Namen auf die IP des Servers zeigen lässt.)"
    read -r -p "Adresse [${suggestion}]: " domain
    domain=${domain:-$suggestion}
    domain=${domain#https://}
    domain=${domain%%/*}
    [[ $domain =~ ^[A-Za-z0-9.-]+\.[A-Za-z]{2,}$ ]] || fail "Das ist keine gültige Adresse: '$domain'"

    read -r -p "Name, der in der iPhone-App angezeigt wird [HealthBridge Server]: " name
    name=${name:-HealthBridge Server}
    name=${name//[\"\$\`\\]/}

    umask 077
    cat >"$env_file" <<EOF
# Von deploy/setup.sh geschrieben. Enthält das Relay-Token: nicht weitergeben.
HB_DOMAIN=$domain
HB_AGENT_NAME="$name"
HB_RELAY_REGISTRATION_TOKEN=$(random_hex)
EOF
    # shellcheck disable=SC1090
    source "$env_file"
    say "Gespeichert in deploy/.env"
fi

# --- Prüfen, ob die Adresse auf diesen Server zeigt ---------------------------------
resolved=$(getent ahostsv4 "$HB_DOMAIN" 2>/dev/null | awk 'NR==1 {print $1}' || true)
ip=${ip:-$(public_ip)}
if [[ -z $resolved ]]; then
    fail "Die Adresse $HB_DOMAIN ist im Internet nicht bekannt. Bei eigener Domain: A-Eintrag auf ${ip:-die Server-IP} setzen und ein paar Minuten warten."
elif [[ -n $ip && $resolved != "$ip" ]]; then
    echo "Achtung: $HB_DOMAIN zeigt auf $resolved, dieser Server hat aber die IP $ip."
    echo "HTTPS funktioniert nur, wenn die Adresse auf diesen Server zeigt."
fi

# --- Bauen und starten -------------------------------------------------------------
say "Baue HealthBridge (beim ersten Mal 2–5 Minuten)…"
compose build
compose up -d relay caddy

say "Warte auf das HTTPS-Zertifikat für https://$HB_DOMAIN …"
for _ in $(seq 1 60); do
    if curl -fsS --max-time 5 "https://$HB_DOMAIN/healthz" >/dev/null 2>&1; then
        ready=1
        break
    fi
    sleep 3
done
if [[ -z ${ready:-} ]]; then
    compose logs --tail 30 caddy || true
    fail "https://$HB_DOMAIN antwortet nicht. Häufigste Ursache: Ports 80 und 443 sind in einer Firewall des Anbieters gesperrt (siehe docs/SERVER_START.md, „Wenn etwas hakt“)."
fi
echo "✓ HTTPS läuft."

# --- Agent einrichten (nur beim ersten Mal) -------------------------------------------
if compose run --rm --no-deps -T agent status --offline >/dev/null 2>&1; then
    say "Agent ist bereits eingerichtet."
    first_run=""
else
    say "Richte den Agenten ein…"
    compose run --rm --no-deps agent onboard \
        --relay "https://$HB_DOMAIN" --relay-token "$HB_RELAY_REGISTRATION_TOKEN" --name "$HB_AGENT_NAME"
    first_run=1
fi
compose up -d agent

connector=$(compose exec -T agent /usr/local/bin/healthbridge connector-url)

say "Fertig. HealthBridge läuft auf https://$HB_DOMAIN"
cat <<EOF

1. iPhone koppeln
   ${first_run:+Den QR-Code oben mit der HealthBridge-App scannen ("Get started").}
   QR-Code erneut anzeigen:   ./deploy/hb pairing

2. Connector-Adresse für ChatGPT und Claude (wie ein Passwort behandeln!):

   $connector

   Wo du sie in ChatGPT, Claude und Claude Code einträgst, steht Schritt für Schritt
   in docs/SERVER_START.md (Schritt 6).

3. Nützlich
   Status:              ./deploy/hb status
   Zusammenfassung:     ./deploy/hb summary --period week
   Neue Geheimadresse:  ./deploy/hb connector-url --rotate
   Aktualisieren:       git pull && sudo ./deploy/setup.sh
EOF
