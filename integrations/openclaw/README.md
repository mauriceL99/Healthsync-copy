# HealthBridge for OpenClaw

```bash
cp -r healthbridge ~/.openclaw/skills/      # or <workspace>/skills/
```

The skill needs the `healthbridge` binary on the `PATH` (`requires.bins`). OpenClaw
can also use the MCP server `healthbridge mcp` directly.

A daily summary, but only if you want one, for example in a chat channel:
"Every morning at 7:30, run `healthbridge fetch && healthbridge summary --period day
--format markdown` and send me the result."
