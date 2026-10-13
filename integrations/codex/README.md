# HealthBridge for Codex

1. Install the CLI (`go install github.com/mauriceL99/Healthsync-copy/cmd/healthbridge@latest`)
   and pair your iPhone with `healthbridge onboard --relay https://…`.
2. Register the MCP server in `~/.codex/config.toml`:

   ```toml
   [mcp_servers.healthbridge]
   command = "healthbridge"
   args = ["mcp"]
   ```

   Or run `codex mcp add healthbridge -- healthbridge mcp`.
3. Optional: copy the skill so that Codex knows the rules and the pairing flow:

   ```bash
   mkdir -p ~/.codex/skills && cp -r skills/healthbridge ~/.codex/skills/
   ```

Example prompt: "Summarize my training load over the last 14 days and compare my
heart-rate zones with the previous two weeks."
