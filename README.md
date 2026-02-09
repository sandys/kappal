# Kappal

**Docker Compose CLI for Kubernetes** - Run your `docker-compose.yaml` on Kubernetes without learning Kubernetes.

[![Conformance Tests](https://img.shields.io/badge/conformance-11%2F11%20passing-brightgreen)]()

## The Name

*Kappal* (கப்பல்) means "ship" in Tamil. The name honors [V.O. Chidambaram Pillai](https://en.wikipedia.org/wiki/V._O._Chidambaram_Pillai), known as *Kappalottiya Tamizhan* ("The Tamil Helmsman") - a freedom fighter who founded India's first indigenous shipping company.

The nautical theme connects to Kubernetes itself: *Kubernetes* (κυβερνήτης) is Greek for "helmsman" or "pilot" - the person who steers a ship. Kappal steers your containers on the Kubernetes seas, so you don't have to learn navigation.

## Overview

Kappal lets you use familiar Docker Compose commands while running your services on Kubernetes (K3s). Users never see kubectl, YAML manifests, or Kubernetes concepts - just the same `up`, `down`, `ps`, `logs`, and `exec` commands they already know.

```bash
kappal up -d                    # Start services
kappal up --build -d            # Build images then start
kappal up --timeout 600 -d      # Custom readiness timeout
kappal ps                       # List services
kappal logs api                 # View logs
kappal exec web sh              # Shell into service
kappal inspect                  # Machine-readable JSON state
kappal down                     # Stop services
```

## Features

- **Zero Kubernetes Knowledge Required** - Use Docker Compose syntax, get Kubernetes benefits
- **Persistent Volumes** - Named volumes survive restarts (`kappal down` + `kappal up`)
- **Service Discovery** - Services find each other by name (just like Docker Compose)
- **Secrets & Configs** - Mount secrets and config files the Compose way
- **Scaling** - Use `deploy.replicas` to scale services
- **Network Isolation** - Define networks to isolate service groups
- **UDP Support** - Full protocol support including UDP ports
- **Dependency Ordering** - `depends_on` with `service_completed_successfully` for Jobs
- **One-Shot Services** - `restart: "no"` runs as K8s Jobs (migrations, seeds, etc.)
- **Profiles** - Services with `profiles` excluded from default `up`
- **Worktree-Safe Naming** - Each directory gets a unique project name (hash-based), so git worktrees or copies with the same basename don't collide
- **Label-Based Discovery** - K3s containers and networks are stamped with `kappal.io/project` labels, so commands find infrastructure reliably regardless of naming conventions
- **Verbose Conformance Tests** - `make conformance` shows timestamped command output, per-test timing, and full diagnostic dumps (K3s logs, pod events, container state) on any failure

## Dependency Ordering & One-Shot Services

Kappal supports `depends_on` with `service_completed_successfully`, letting you run migrations, seeds, and setup tasks that must finish before dependent services start.

```yaml
services:
  migrate:
    image: myapp:latest
    command: ["./migrate", "up"]
    restart: "no"                    # Runs as a K8s Job (exits when done)

  app:
    image: myapp:latest
    depends_on:
      migrate:
        condition: service_completed_successfully  # Waits for migrate to finish
```

**How it works:**

- Services with `restart: "no"` become Kubernetes Jobs (not Deployments), so they run once and stop cleanly instead of restarting in a loop.
- When a service depends on a Job with `condition: service_completed_successfully`, Kappal injects an init container that waits for the Job to complete before starting the dependent service.
- Failed Job pods from K8s retries don't block readiness — only the latest attempt matters.
- Services with `profiles` are excluded from `kappal up` by default, matching Docker Compose behavior.
- In detach mode (`-d`), readiness timeout is a warning, not a fatal error. Use `--timeout` to adjust for complex stacks.

## Prerequisites

**Only Docker is required.** Kappal handles everything else automatically.

| Requirement | Notes |
|-------------|-------|
| Docker | Only prerequisite - [Install Docker](https://docs.docker.com/get-docker/) |
| ~~Kubernetes~~ | Not needed - Kappal runs K3s automatically |
| ~~kubectl~~ | Not needed - included in Kappal image |
| ~~K3s~~ | Not needed - runs as a container |

## Installation

```bash
# 1. Install Docker (if not already installed)
curl -fsSL https://get.docker.com | sh

# 2. Pull kappal image
docker pull ghcr.io/sandys/kappal:latest

# 3. Add alias (for current session)
alias kappal='docker run --rm -v /var/run/docker.sock:/var/run/docker.sock -v "$(pwd):/project" -w /project -e KAPPAL_HOST_DIR="$(pwd)" --network host ghcr.io/sandys/kappal:latest'

# Or save permanently to ~/.bashrc or ~/.zshrc
echo "alias kappal='docker run --rm -v /var/run/docker.sock:/var/run/docker.sock -v \"\$(pwd):/project\" -w /project -e KAPPAL_HOST_DIR=\"\$(pwd)\" --network host ghcr.io/sandys/kappal:latest'" >> ~/.bashrc
source ~/.bashrc
```

That's it. You're ready to use Kappal.

## Quick Start

```bash
# Navigate to your project with docker-compose.yaml
cd /path/to/your/project

# First-time setup (required once per project)
kappal --setup

# Start services in detached mode
kappal up -d

# Check status
kappal ps

# View logs
kappal logs

# View logs for specific service
kappal logs api

# Shell into a service
kappal exec web sh

# Stop everything
kappal down

# Stop and remove volumes
kappal down -v
```

## Commands

| Command | Description |
|---------|-------------|
| `kappal --setup` | Set up kappal for this project (required first time) |
| `kappal up [-d]` | Create and start services (timeout is a warning in detach mode) |
| `kappal up --build` | Build images and start services |
| `kappal up --timeout 600` | Custom readiness timeout in seconds (default 300) |
| `kappal down [-v]` | Stop and remove services (-v removes volumes) |
| `kappal ps` | List running services |
| `kappal logs [service]` | View service logs |
| `kappal exec <service> <cmd>` | Execute command in service |
| `kappal build` | Build images from Dockerfiles |
| `kappal inspect` | Show project state as self-documenting JSON |
| `kappal clean` | Remove kappal workspace and K3s |
| `kappal eject` | Export as standalone Tanka workspace |

## Compose Features Supported

| Feature | Status | Example |
|---------|--------|---------|
| Services | ✅ | `services.web.image: nginx` |
| Ports | ✅ | `ports: ["8080:80"]` |
| Volumes (named) | ✅ | `volumes: [data:/var/lib/data]` |
| Environment | ✅ | `environment: [KEY=value]` |
| Secrets | ✅ | `secrets: [my_secret]` |
| Configs | ✅ | `configs: [app_config]` |
| Networks | ✅ | `networks: [frontend, backend]` |
| Scaling | ✅ | `deploy.replicas: 3` |
| Build | ✅ | `build: ./app` |
| Custom Dockerfile | ✅ | `build.dockerfile: Dockerfile.prod` |
| Command | ✅ | `command: ["npm", "start"]` |
| Entrypoint | ✅ | `entrypoint: ["/docker-entrypoint.sh"]` |
| UDP ports | ✅ | `ports: ["53:53/udp"]` |
| Depends On | ✅ | `depends_on: {db: {condition: service_completed_successfully}}` |
| One-Shot Services (Jobs) | ✅ | `restart: "no"` runs as a K8s Job |
| Profiles | ✅ | `profiles: [debug]` excluded from default `up` |
| Healthchecks | 🚧 | Planned |

**Note:** Duplicate container port/protocol across services (e.g. two services both exposing `80/tcp`) is rejected with an error.

## Examples

### Compose File in Subdirectory

If your `docker-compose.yml` is in a subdirectory (e.g., `deploy/docker-compose/`), use the `-f` flag:

```bash
# Project structure:
# myproject/
# ├── apps/
# ├── packages/
# └── deploy/
#     └── docker-compose/
#         └── docker-compose.yml

# Option 1: Use -f flag with relative path
kappal -f deploy/docker-compose/docker-compose.yml up

# Option 2: cd into the directory
cd deploy/docker-compose
kappal -f docker-compose.yml up
```

### Monorepo / Custom Build Contexts

If your `docker-compose.yml` references parent directories (e.g., `build: context: ../..`), you need to mount from the project root and set the working directory:

```bash
# For monorepos, create a project-specific alias
alias kappal-myproject='docker run --rm -v /var/run/docker.sock:/var/run/docker.sock -v "/path/to/project/root:/project" -w /project/path/to/compose/dir -e KAPPAL_HOST_DIR="/path/to/project/root" --network host ghcr.io/sandys/kappal:latest'

# Then use normally
kappal-myproject up --build
```

## Programmatic Access (`kappal inspect`)

`kappal inspect` outputs a self-documenting JSON object combining compose file service definitions with live K8s and Docker runtime state — ports, replicas, pod IPs, and K3s container info. The JSON includes a `_schema` field describing every data field. If K3s is running but the API is unreachable, services are listed with status `"unavailable"`. Services in the compose file but not deployed show status `"missing"`. For Deployments, only Running/Pending pods are shown (historical completed/failed pods are filtered out). For Jobs, all pods are shown including Succeeded/Failed to reflect execution history.

```bash
# Full project state
kappal inspect

# Get host port for a service
kappal inspect | jq '.services[] | select(.name=="web") | .ports[0].host'

# Check if all services are running
kappal inspect | jq '[.services[] | .status] | all(. == "running")'

# List pod IPs
kappal inspect | jq '.services[] | select(.name=="api") | .pods[].ip'

# Dynamic port resolution for testing
PORT=$(kappal inspect | jq '.services[] | select(.name=="web") | .ports[0].host')
curl http://localhost:$PORT/health
```

Use `inspect` instead of `ps` when you need machine-readable data. The `ps` command is better for quick human-readable status checks.

## AI Agent / Claude Code Integration

Kappal includes a skill file ([`skills/kappal/SKILL.md`](skills/kappal/SKILL.md)) that lets [Claude Code](https://docs.anthropic.com/en/docs/agents-and-tools/claude-code/overview) and other AI coding agents deploy docker-compose projects to Kubernetes autonomously.

**How it works:** Claude reads the skill file and handles the full lifecycle — setup, build, deploy, logs, teardown — without the user needing to know kappal internals.

**What the user says:** Just tell Claude "deploy this with kappal" or "run this docker-compose in kappal" and it handles the rest.

**Self-updating:** The skill auto-fetches the latest version from GitHub at the start of each conversation, so it stays current with breaking changes and new features.

**No other container orchestration tool offers native AI agent integration** — docker compose, podman, and others require the user to know the CLI. Kappal works with AI agents out of the box.

## Project Naming

Kappal derives the project name from the compose file's directory path: `<basename>-<8-char-hash>`. This means two directories named `myapp` in different locations (e.g. git worktrees) get distinct project names and never interfere with each other. When running directly on the host, symlinks to the same physical directory produce the same name. In Docker wrapper mode, the caller should pass a resolved (canonical) path via `KAPPAL_HOST_DIR`.

Override with `-p <name>` if you need a specific project name.

## How It Works

```
docker-compose.yaml
        │
        ▼
┌───────────────────┐
│   compose-go     │  Parse compose file
└───────────────────┘
        │
        ▼
┌───────────────────┐
│   Transformer    │  Convert to K8s manifests
└───────────────────┘
        │
        ▼
┌───────────────────┐
│   K3s (Docker)   │  Lightweight Kubernetes
└───────────────────┘
        │
        ▼
    Your services running on Kubernetes!
```

**Key Design Principles:**

1. **Users never see Kubernetes** - All K8s concepts are hidden behind Compose semantics
2. **Self-contained** - K3s runs in Docker, no system installation needed
3. **Persistent by default** - Volumes survive `down`/`up` cycles (use `-v` to remove)
4. **Standard tools** - Uses compose-go (official parser), K3s, client-go
5. **Label-based discovery** - Infrastructure is found via Docker labels, not naming conventions

## Development

```bash
# Clone the repo
git clone https://github.com/kappal-app/kappal.git
cd kappal

# Build Docker image
make docker-build

# Run unit tests
make test

# Run conformance tests (all 11 must pass)
make conformance

# Run all lints
make lint-all
```

### Conformance Tests

Kappal passes all 11 conformance tests based on the [compose-spec](https://github.com/compose-spec/compose-spec):

- SimpleLifecycle - Basic up/down
- SimpleNetwork - Service-to-service DNS
- VolumeFile - Persistent volume data
- SecretFile - Secret mounting
- ConfigFile - Config file mounting
- UdpPort - UDP protocol support
- Scaling - Replica scaling
- DifferentNetworks - Network isolation
- JobLifecycle - One-shot services run as Jobs and complete
- DependencyOrdering - `service_completed_successfully` ordering via init containers
- ProfileExclusion - Profiled services excluded from default `up`

Test output is fully verbose: every command shows timestamped stdout/stderr, per-test elapsed time, and on any failure a full diagnostic dump (kappal ps/inspect, Docker containers, K3s logs, kubectl events, pod descriptions).

## FAQ

**Q: Why not just use Kompose?**
A: Kompose converts Compose files to K8s manifests, but you still need to manage Kubernetes. Kappal hides K8s completely - same CLI experience as Docker Compose.

**Q: Why K3s instead of kind/minikube?**
A: K3s is lightweight, fast to start, and includes essentials like ServiceLB and local-path-provisioner out of the box.

**Q: Can I see the generated Kubernetes manifests?**
A: Yes, they're in `.kappal/manifests/all.yaml` (but you shouldn't need to).

**Q: How do I debug issues?**
A: Use `kappal logs <service>` and `kappal exec <service> sh`. If you need deeper debugging, the kubeconfig is at `.kappal/runtime/kubeconfig.yaml`.

## License

MIT
