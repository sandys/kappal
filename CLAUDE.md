# Claude Code Instructions

## Running Kappal

**HARD REQUIREMENT:** Always run kappal from within a Docker container using `make` targets. Never run the `kappal` binary directly on the host.

Use these commands:
- `make conformance` - Run conformance tests (runs kappal in Docker)
- `make dev-test` - Run dev tests (runs kappal in Docker)

For manual testing, use the kappal-builder container:
```bash
docker run --rm -v /var/run/docker.sock:/var/run/docker.sock -v $(pwd):/workspace -w /workspace -e KAPPAL_HOST_DIR="$(pwd)" ghcr.io/sandys/kappal:latest kappal [command]
```

## Skill File Maintenance

`skills/kappal/SKILL.md` is the canonical skill file for AI agent integration. It is **distinct from the README**:
- **README** = marketing/product documentation for humans
- **Skill file** = structured instructions for AI agents (Claude Code, etc.)

When the README is updated with new commands, features, or usage patterns, **the skill file must also be updated** to reflect those changes. The two files cover overlapping content but serve different audiences.

The skill file auto-updates from GitHub (`https://raw.githubusercontent.com/sandys/kappal/main/skills/kappal/SKILL.md`) once per conversation when used by an AI agent. This means the GitHub version is the source of truth for deployed agents.

## AI Readability Mandate

All kappal command help text (`--help`) MUST be detailed enough for AI tools to use
autonomously — including flags, output format, examples, and when to use each command.

`kappal inspect` JSON output includes a `_schema` field with descriptions for every
data field. When modifying inspect output, always update the `inspectSchema` map in
`cmd/kappal/inspect.go`. When adding new commands, write Long descriptions that an
AI agent can act on without external documentation.
