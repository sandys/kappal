# Kappal

**Docker Compose CLI for Kubernetes** - Run your `docker-compose.yaml` on Kubernetes without learning Kubernetes.

[![Conformance Tests](https://img.shields.io/badge/conformance-8%2F8%20passing-brightgreen)]()

## The Name

*Kappal* (கப்பல்) means "ship" in Tamil. The name honors [V.O. Chidambaram Pillai](https://en.wikipedia.org/wiki/V._O._Chidambaram_Pillai), known as *Kappalottiya Tamizhan* ("The Tamil Helmsman") - a freedom fighter who founded India's first indigenous shipping company.

The nautical theme connects to Kubernetes itself: *Kubernetes* (κυβερνήτης) is Greek for "helmsman" or "pilot" - the person who steers a ship. Kappal steers your containers on the Kubernetes seas, so you don't have to learn navigation.

## Overview

Kappal lets you use familiar Docker Compose commands while running your services on Kubernetes (K3s). Users never see kubectl, YAML manifests, or Kubernetes concepts - just the same `up`, `down`, `ps`, `logs`, and `exec` commands they already know.

```bash
kappal up -d                    # Start services
kappal ps                       # List services
kappal logs api                 # View logs
kappal exec web sh              # Shell into service
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
docker pull ghcr.io/kappal-app/kappal:latest

# 3. Add alias to your shell (~/.bashrc or ~/.zshrc)
echo 'alias kappal='\''docker run --rm -v /var/run/docker.sock:/var/run/docker.sock -v "$(pwd):/project" -w /project --network host ghcr.io/kappal-app/kappal:latest'\''' >> ~/.bashrc

# 4. Reload shell
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
| `kappal up [-d]` | Create and start services |
| `kappal up --build` | Build images and start services |
| `kappal down [-v]` | Stop and remove services (-v removes volumes) |
| `kappal ps` | List running services |
| `kappal logs [service]` | View service logs |
| `kappal exec <service> <cmd>` | Execute command in service |
| `kappal build` | Build images from Dockerfiles |
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
| Depends On | ⚠️ | Partial (ordering only) |
| Healthchecks | 🚧 | Planned |

## Monorepo / Custom Build Contexts

If your `docker-compose.yml` references parent directories (e.g., `build: context: ../..`), you need to mount from the project root and set the working directory:

```bash
# For monorepos, create a project-specific alias
alias kappal-myproject='docker run --rm -v /var/run/docker.sock:/var/run/docker.sock -v "/path/to/project/root:/project" -w /project/path/to/compose/dir --network host ghcr.io/kappal-app/kappal:latest'

# Then use normally
kappal-myproject up --build
```

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

## Development

```bash
# Clone the repo
git clone https://github.com/kappal-app/kappal.git
cd kappal

# Build Docker image
make docker-build

# Run unit tests
make test

# Run conformance tests (all 8 must pass)
make conformance

# Run all lints
make lint-all
```

### Conformance Tests

Kappal passes all 8 conformance tests based on the [compose-spec](https://github.com/compose-spec/compose-spec):

- SimpleLifecycle - Basic up/down
- SimpleNetwork - Service-to-service DNS
- VolumeFile - Persistent volume data
- SecretFile - Secret mounting
- ConfigFile - Config file mounting
- UdpPort - UDP protocol support
- Scaling - Replica scaling
- DifferentNetworks - Network isolation

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
