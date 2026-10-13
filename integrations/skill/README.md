# Generic skill (Hermes Agent and other Agent Skills clients)

`healthbridge/SKILL.md` follows the Agent Skills format (YAML frontmatter with
`name` and `description`). Copy the folder into your agent's skills directory, for
example `~/.hermes/skills/`, and put the `healthbridge` binary on the `PATH`.

This file is the source for the copies in `integrations/claude-code`,
`integrations/codex` and `integrations/openclaw`. Run `make skills` after changing it.
