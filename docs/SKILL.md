# Laevitas CLI — Agent Skill

> **This document moved.** The canonical agent skill now lives at
> [`skills/laevitas-cli/SKILL.md`](../skills/laevitas-cli/SKILL.md), structured as a
> proper [Agent Skill](https://www.anthropic.com/engineering/equipping-agents-for-the-real-world-with-agent-skills):
> a short, front-loaded `SKILL.md` plus topic reference files under
> [`skills/laevitas-cli/reference/`](../skills/laevitas-cli/reference/).
>
> AI assistants (Claude Code, Cursor, Codex, …) can install it directly:
>
> ```bash
> npx skills add laevitas/cli
> ```

## Why the move

The old single-file skill had grown past 700 lines — a reference dump that an
agent had to read in full before doing anything. The new layout follows
Anthropic's skill-authoring guidance: the `SKILL.md` leads with the handful of
rules that prevent the most common agent failures and a copy-pasteable quick
start, then points to reference files that load **only when the task needs
them** (progressive disclosure).

## Map of the new skill

| Topic | File |
|---|---|
| The five failure-preventing rules + quick start | [`SKILL.md`](../skills/laevitas-cli/SKILL.md) |
| Every command group, subcommand, instrument format | [`reference/commands.md`](../skills/laevitas-cli/reference/commands.md) |
| REST envelope + stable error codes | [`reference/response-shape.md`](../skills/laevitas-cli/reference/response-shape.md) |
| API key vs x402 wallet, budget loops | [`reference/auth.md`](../skills/laevitas-cli/reference/auth.md) |
| Time-series / catalog / snapshot flags, market & margin tokens | [`reference/parameters.md`](../skills/laevitas-cli/reference/parameters.md) |
| Live `ws` streaming: channels, wildcards, discriminators | [`reference/streaming.md`](../skills/laevitas-cli/reference/streaming.md) |
| Order books — snapshot vs stats shape, REST/WS parity | [`reference/orderbooks.md`](../skills/laevitas-cli/reference/orderbooks.md) |
| `dash` TUI dashboards (human-only) + agent equivalents | [`reference/dashboards.md`](../skills/laevitas-cli/reference/dashboards.md) |

When adding a command or flag, update the relevant file under
`skills/laevitas-cli/` (and `README.md`) — that directory is now the source of
truth for agent-facing CLI guidance.
