# Kappal

**Docker Compose CLI for Kubernetes** - Run your `docker-compose.yaml` on Kubernetes without learning Kubernetes.

[![Conformance Tests](https://img.shields.io/badge/conformance-8%2F8%20passing-brightgreen)]()

## Overview

Kappal lets you use familiar Docker Compose commands while running your services on Kubernetes (K3s). Users never see kubectl, YAML manifests, or Kubernetes concepts - just the same `up`, `down`, `ps`, `logs`, and `exec` commands they already know.

```bash
# Instead of learning Kubernetes...
kappal up -d                    # Start services (like docker compose up -d)
kappal ps                       # List services (like docker compose ps)
kappal logs api                 # View logs (like docker compose logs)
kappal exec web sh              # Shell into service (like docker compose exec)
kappal down                     # Stop services (like docker compose down)
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

When you run Kappal, it automatically:
- Pulls and starts K3s (lightweight Kubernetes)
- Builds your application images
- Loads images into K3s
- Deploys your services

## Installation

### Fresh laptop setup

```bash
# 1. Install Docker (if not already installed)
curl -fsSL https://get.docker.com | sh

# 2. Pull kappal image
docker pull kappal/kappal:latest
```

That's it. You're ready to use Kappal.

## Quick Start

```bash
# Navigate to your project with docker-compose.yaml
cd /path/to/your/project

# Start services
docker run --rm \
  -v /var/run/docker.sock:/var/run/docker.sock \
  -v "$(pwd):/project" \
  -w /project \
  --network host \
  kappal/kappal:latest up -d

# Check status
docker run --rm \
  -v /var/run/docker.sock:/var/run/docker.sock \
  -v "$(pwd):/project" \
  -w /project \
  --network host \
  kappal/kappal:latest ps

# View logs
docker run --rm \
  -v /var/run/docker.sock:/var/run/docker.sock \
  -v "$(pwd):/project" \
  -w /project \
  --network host \
  kappal/kappal:latest logs

# Stop everything
docker run --rm \
  -v /var/run/docker.sock:/var/run/docker.sock \
  -v "$(pwd):/project" \
  -w /project \
  --network host \
  kappal/kappal:latest down
```

### Recommended: Create an alias

Add to your `~/.bashrc` or `~/.zshrc`:

```bash
alias kappal='docker run --rm -v /var/run/docker.sock:/var/run/docker.sock -v "$(pwd):/project" -w /project --network host kappal/kappal:latest'
```

Then use it like Docker Compose:

```bash
kappal up -d
kappal ps
kappal logs api
kappal exec web sh
kappal down
```

### Monorepo / Custom build contexts

If your `docker-compose.yml` references parent directories (e.g., `build: context: ../..`), mount from the project root:

```bash
cd /path/to/project/root

docker run --rm \
  -v /var/run/docker.sock:/var/run/docker.sock \
  -v "$(pwd):/project" \
  -w /project/path/to/compose/dir \
  --network host \
  kappal/kappal:latest up --build -f docker-compose.yml
```

### Building from source

```bash
# Clone the repo
git clone https://github.com/kappal-app/kappal.git
cd kappal

# Build Docker image
make docker-build

# Or build binary (requires Go 1.22+)
go build -o kappal ./cmd/kappal
```

## Commands

| Command | Description |
|---------|-------------|
| `kappal up [-d]` | Create and start services |
| `kappal down [-v]` | Stop and remove services (-v removes volumes) |
| `kappal ps` | List running services |
| `kappal logs [service]` | View service logs |
| `kappal exec <service> <cmd>` | Execute command in service |
| `kappal build` | Build images from Dockerfiles |
| `kappal clean` | Remove kappal workspace and K3s |

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

## Architecture

```
kappal/
├── cmd/kappal/          # CLI commands (up, down, ps, logs, exec)
├── pkg/
│   ├── compose/         # compose-go wrapper
│   ├── transform/       # Compose → K8s manifest transformer
│   ├── k3s/             # K3s lifecycle management
│   ├── k8s/             # client-go wrapper for status, logs, exec
│   ├── tanka/           # kubectl apply/delete wrapper
│   └── workspace/       # .kappal/ directory management
├── scripts/
│   ├── conformance-test.sh
│   └── lint-*.sh        # Code quality checks
└── testdata/            # Conformance test fixtures
```

## Development

```bash
# Build Docker image
make docker-build

# Run conformance tests (all 8 must pass)
make conformance

# Run all lints
make lint-all

# Format code
make fmt
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

### Lint Checks

```bash
make lint-ux       # Ensure kubectl not exposed to users
make lint-compose  # Ensure compose features fully supported
make lint-adhoc    # Ensure no ad-hoc Docker workarounds
make lint-k8s      # Ensure correct K8s patterns (command/args, Services for DNS)
make lint-volumes  # Ensure volume persistence (down preserves, down -v removes)
make lint-all      # Run all lints
```

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
