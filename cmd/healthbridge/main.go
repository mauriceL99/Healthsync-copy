// Command healthbridge is the agent-side CLI of HealthBridge: it pairs with
// the iPhone app, fetches end-to-end encrypted Apple Health data from the
// relay and makes it available to AI agents (summaries, dashboards, MCP).
package main

import (
	"os"

	"github.com/mauriceL99/Healthsync-copy/internal/agent"
)

func main() {
	os.Exit(agent.Run(os.Args[1:], agent.Env{Out: os.Stdout, Err: os.Stderr}))
}
