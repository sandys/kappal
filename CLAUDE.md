# Claude Code Instructions

## Running Kappal

**HARD REQUIREMENT:** Always run kappal from within a Docker container using `make` targets. Never run the `kappal` binary directly on the host.

Use these commands:
- `make conformance` - Run conformance tests (runs kappal in Docker)
- `make dev-test` - Run dev tests (runs kappal in Docker)

For manual testing, use the kappal-builder container:
```bash
docker run --rm -v /var/run/docker.sock:/var/run/docker.sock -v $(pwd):/workspace -w /workspace ghcr.io/sandys/kappal:latest kappal [command]
```
