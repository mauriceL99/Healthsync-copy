# HealthBridge for ChatGPT (and Claude on the web)

ChatGPT runs in the cloud, so it cannot start a program on your computer. HealthBridge
therefore runs the agent as a service on a small server (`healthbridge serve`) and
ChatGPT connects to it over HTTPS (MCP, Streamable HTTP).

1. Set up the server: `sudo ./deploy/setup.sh` on a fresh Ubuntu server. The full
   beginner guide (German) is [`docs/SERVER_START.md`](../../docs/SERVER_START.md).
2. Print the secret connector URL on the server: `./deploy/hb connector-url`.
3. ChatGPT (Plus or higher): Settings → Apps & Connectors → Advanced → enable
   Developer mode → Create. Paste the URL, choose **No authentication**.
4. Claude: Settings → Connectors → Add custom connector → paste the same URL.

The URL contains a 256-bit secret and is the only credential: anyone who has it can
read your health data. Replace it with `./deploy/hb connector-url --rotate`.

All ten tools are the same as for the local `healthbridge mcp`: six for Apple Health
(`health_*`) and four for Garmin workouts imported from watch files (`training_*`).
The query tools are marked read-only, so ChatGPT only asks for confirmation before
`health_sync`. Garmin files are added on the upload page,
`./deploy/hb connector-url --upload` (guide: [`docs/GARMIN.md`](../../docs/GARMIN.md)).
After an update that adds tools, refresh the connector in ChatGPT's settings.
