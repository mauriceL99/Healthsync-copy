# HealthBridge for Claude Code

This plugin adds three things to Claude Code:

* the `healthbridge` MCP server (`healthbridge mcp`) with the tools `health_summary`,
  `health_metric_series`, `health_workouts`, `health_list_metrics`, `health_status`
  and `health_sync`
* the `healthbridge` skill (pairing, rules for health data)
* the `/health [day|week|month]` command

## Requirements

The `healthbridge` CLI must be on your `PATH`:

```bash
go install github.com/mauriceL99/Healthsync-copy/cmd/healthbridge@latest
# or: make build && cp bin/healthbridge /usr/local/bin/
```

## Installation

```text
/plugin marketplace add mauriceL99/Healthsync-copy
/plugin install healthbridge@healthbridge
```

Or run it locally from a clone: `claude --plugin-dir integrations/claude-code/healthbridge`.

Without the plugin, you can register only the MCP server:

```bash
claude mcp add healthbridge -- healthbridge mcp
```

Then pair your iPhone once (`healthbridge onboard --relay https://…`) and ask, for
example: "How did I sleep this week compared to last week?"
