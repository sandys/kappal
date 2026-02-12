---
name: kappal
description: Deploy docker-compose projects to Kubernetes using Kappal. Use when the user wants to run docker-compose on Kubernetes, deploy with kappal, or says "make this run in kappal". Handles setup, build, deploy, logs, and teardown autonomously.
---

# Kappal Skill — Docker Compose CLI for Kubernetes

## 0. Self-Update

**On the first kappal-related task in a conversation**, fetch the latest version of this skill:

```
WebFetch: https://raw.githubusercontent.com/sandys/kappal/main/skills/kappal/SKILL.md
```

- If the fetch succeeds, overwrite the local `skills/kappal/SKILL.md` with the fetched content and use the updated version for all subsequent actions.
- If the fetch fails (offline, rate-limited, timeout), warn the user: "Could not fetch latest kappal skill from GitHub. Proceeding with local copy." Then continue with the local content.
- When user says **"update kappal skill"** or **"kappal update"**, fetch and rewrite `skills/kappal/SKILL.md` unconditionally.

---

## 1. Overview

Kappal runs `docker-compose.yaml` on Kubernetes (K3s) without requiring any Kubernetes knowledge. **Docker is the only prerequisite.**

---

## 2. How to Invoke Kappal

**Zero installation footprint.** No aliases, no shell changes, no wrapper scripts. Invoke via full `docker run` command every time.

Always pull the latest image first:
```bash
docker pull ghcr.io/sandys/kappal:latest
```

Base command template:
```bash
docker run --rm \
  -v /var/run/docker.sock:/var/run/docker.sock \
  -v "<project-root>:/project" \
  -w /project/<compose-dir> \
  -e KAPPAL_HOST_DIR="<project-root>" \
  --network host \
  ghcr.io/sandys/kappal:latest <command>
```

The `-v` mount and `-w` working directory vary by scenario — see Section 3.

---

## 3. Scenario Templates

### Scenario A — Simple (compose file in project root)

The compose file is at the project root (e.g., `./docker-compose.yml`).

```bash
docker run --rm \
  -v /var/run/docker.sock:/var/run/docker.sock \
  -v "$(pwd):/project" \
  -w /project \
  -e KAPPAL_HOST_DIR="$(pwd)" \
  --network host \
  ghcr.io/sandys/kappal:latest <command>
```

### Scenario B — Compose in subdirectory

The compose file is in a subdirectory (e.g., `deploy/docker-compose/docker-compose.yml`), but all build contexts are within the project root.

```bash
docker run --rm \
  -v /var/run/docker.sock:/var/run/docker.sock \
  -v "$(pwd):/project" \
  -w /project/deploy/docker-compose \
  -e KAPPAL_HOST_DIR="$(pwd)" \
  --network host \
  ghcr.io/sandys/kappal:latest <command>
```

Project root is mounted; working directory is set to the compose file's directory.

### Scenario C — Monorepo (build contexts reference parent directories)

The compose file uses `build: context: ../..` or similar parent-directory references. The mount point must be the **highest ancestor** that any `build.context` references.

```bash
docker run --rm \
  -v /var/run/docker.sock:/var/run/docker.sock \
  -v "/absolute/path/to/monorepo/root:/project" \
  -w /project/deploy/docker-compose \
  -e KAPPAL_HOST_DIR="/absolute/path/to/monorepo/root" \
  --network host \
  ghcr.io/sandys/kappal:latest <command>
```

**IMPORTANT:** Always confirm the detected mount root path with the user before running.

---

## 4. Autonomous Workflow

When the user says "make this run in kappal", "deploy this with kappal", or similar:

### Drop-in replacement policy (mandatory)

- Treat the existing `docker-compose` file and env files as the source of truth.
- Start with the command equivalent of what the user would run in Docker Compose (`up -d`, `up --build -d`, etc.).
- Do **not** rewrite the compose file, add kappal-specific services, or port shell scripts into compose unless the user explicitly asks for that transformation.
- Treat `kappal up` compatibility findings (`Compatibility check: ...`) as first-class diagnostics and report them before suggesting any compose edits.
- If deployment fails, debug with `kappal inspect`, `kappal ps`, `kappal logs`, and `kappal exec` first; propose compose edits only when the same stack is not runnable as written.

### Step 1: Find the compose file

Search for `docker-compose.yml`, `docker-compose.yaml`, `compose.yml`, `compose.yaml` in:
- Current working directory
- Common subdirectories: `deploy/`, `docker/`, `deploy/docker-compose/`, `.docker/`

### Step 2: Read and analyze the compose file

Parse it for:
- **Services with `build:`** — these need the `--build` flag
- **`build.context` paths** — check if any reference parent directories (`..`). This triggers monorepo detection (Scenario C)
- **`env_file:` references** — verify these files exist at the expected paths relative to the compose file
- **Port mappings** — note which ports will be exposed
- **Named volumes** — note persistent data
- **Writable bind mounts** — note bind mounts without `read_only`; kappal auto-enables init-time permission prep for these
- **`deploy.replicas`** — note scaling configuration
- **`restart: "no"`** — these services will run as one-shot Jobs (migrations, seeds, etc.)
- **`depends_on` with conditions** — note any `service_completed_successfully` (Jobs) and `service_healthy` (healthcheck-based) dependencies
- **`healthcheck:`** — note services with healthchecks (translated to K8s readiness probes)
- **`profiles:`** — these services will be excluded from default `up`

### Step 3: Detect scenario

- No `build.context` going above compose dir → **Scenario A** (if compose at root) or **Scenario B** (if in subdirectory)
- Any `build.context` referencing parent dirs → **Scenario C** (monorepo)

Construct the full `docker run` command using the appropriate template from Section 3.

### Step 4: Confirm with user (monorepo only)

If Scenario C is detected, show the user:
- The detected monorepo root path (mount point)
- The compose directory (working directory)
- Ask for confirmation before proceeding

### Step 5: Pull the image

```bash
docker pull ghcr.io/sandys/kappal:latest
```

### Step 6: Run setup (automatic)

Run setup silently — do not ask the user about this step:
```bash
<kappal-docker-run> --setup
```

### Step 7: Show deployment plan

Tell the user:
- Which services will start
- Which services will be built (those with `build:`)
- Which services will run as one-shot Jobs (`restart: "no"`)
- Which services are excluded due to profiles
- Any dependency ordering (`depends_on` with `service_completed_successfully` or `service_healthy`)
- What ports will be exposed
- Any volumes that will be created

### Step 8: Deploy on user approval

On approval:
- If any services have `build:` contexts: `<kappal-docker-run> up --build -d`
- If all services use pre-built images: `<kappal-docker-run> up -d`
- For complex stacks with many sequential Jobs (migrations, seeds): add `--timeout 600` or higher

### Step 9: Verify

Run `<kappal-docker-run> ps` and report service status to the user.

---

## 5. Command Reference

| Docker Compose Equivalent | Kappal Command | Notes |
|---|---|---|
| `docker compose up -d` | `<kappal> up -d` | Start services detached (timeout is a warning, not fatal) |
| `docker compose up --build -d` | `<kappal> up --build -d` | Build images + start |
| N/A | `<kappal> up --timeout 600 -d` | Custom readiness timeout in seconds (default 300) |
| `docker compose down` | `<kappal> down` | Stop services, preserve volumes |
| `docker compose down -v` | `<kappal> down -v` | Stop + remove volumes |
| `docker compose ps` | `<kappal> ps` | List running services |
| `docker compose logs <svc>` | `<kappal> logs <svc>` | View logs for a service |
| `docker compose logs -f <svc>` | `<kappal> logs --follow <svc>` | Stream logs |
| `docker compose exec <svc> sh` | `<kappal> exec <svc> sh` | Shell into a service |
| `docker compose build` | `<kappal> build` | Build all images |
| `docker compose build <svc>` | `<kappal> build <svc>` | Build a specific service |
| N/A | `<kappal> clean` | Remove kappal workspace + K3s for current project |
| N/A | `<kappal> clean --all` | Remove ALL kappal resources system-wide |
| N/A | `<kappal> eject -o tanka/` | Export as standalone Tanka workspace |

| N/A | `<kappal> inspect` | Machine-readable JSON state of the entire project |

### Additional Flags

| Flag | Scope | Description |
|---|---|---|
| `-f <path>` | Global (before command) | Specify compose file path |
| `-p <name>` | Global (before command) | Override project name (default: `<basename>-<8-char-hash>` from compose dir path) |
| `ps -o json` | ps | JSON output |
| `up --timeout 600` | up | Readiness timeout in seconds (default 300) |
| `logs --tail 50` | logs | Last N lines |
| `exec -it` | exec | Interactive TTY |
| `exec --index 2` | exec | Target specific replica |

---

## 5a. Programmatic Inspection (`kappal inspect`)

`kappal inspect` outputs a self-documenting JSON object combining compose file service definitions with live K8s and Docker runtime state. Use it instead of `ps` when you need machine-readable data — ports, pod IPs, replica counts, healthcheck config, or K3s container info. If K3s is running but the API is unreachable, services are listed with status `"unavailable"`. Services in the compose file but not deployed show status `"missing"`. For Deployments, only Running/Pending pods are shown (historical completed/failed pods are filtered out). For Jobs, all pods are shown including Succeeded/Failed to reflect execution history.

### JSON Structure

```json
{
  "_schema": { "...": "field descriptions (see below)" },
  "project": "myapp",
  "k3s": {
    "container": "kappal-myapp-k3s",
    "status": "running",
    "network": "kappal-myapp-net"
  },
  "services": [
    {
      "name": "web",
      "kind": "Deployment",
      "image": "myapp-web:latest",
      "status": "running",
      "replicas": { "ready": 2, "desired": 2 },
      "ports": [
        { "host": 8080, "container": 80, "protocol": "tcp" }
      ],
      "healthcheck": {
        "test": ["CMD-SHELL", "curl -f http://localhost/health"],
        "interval": "10s",
        "timeout": "5s",
        "retries": 3,
        "start_period": "30s"
      },
      "pods": [
        { "name": "web-abc123", "status": "Running", "ip": "10.42.0.5" },
        { "name": "web-def456", "status": "Running", "ip": "10.42.0.6" }
      ]
    }
  ]
}
```

### Field Reference (from `_schema`)

| Field Path | Description |
|---|---|
| `project` | Compose project name, derived from directory name or `-p` flag. Also used as the K8s namespace. |
| `k3s.container` | Docker container name running this project's K3s instance (format: `kappal-<project>-k3s`). |
| `k3s.status` | K3s container state. Values: `running`, `stopped`, `not found`. |
| `k3s.network` | Docker bridge network isolating this project (format: `kappal-<project>-net`). |
| `services[].name` | Service name from docker-compose.yaml. Used as K8s Deployment/Job name and DNS hostname. |
| `services[].kind` | K8s workload type. `Deployment` for long-running services, `Job` for run-to-completion (`restart: no`). |
| `services[].image` | Container image. For locally-built images: `<project>-<service>:latest`. |
| `services[].status` | Aggregated health. Deployment: `running`, `waiting`, `partial`. Job: `completed`, `running`, `failing`, `failed`, `pending`. Other: `missing` (in compose but not in K8s), `unavailable` (K8s API unreachable). |
| `services[].replicas.ready` | Number of pods running and passing readiness checks. |
| `services[].replicas.desired` | Target replica count from `deploy.replicas` (default 1). |
| `services[].ports[].host` | Port number on the Docker host. Use for external access (curl, browser). |
| `services[].ports[].container` | Target port for the K8s Service and container (the compose `target` value). Kappal sets both the K8s Service port and targetPort to this value. |
| `services[].ports[].protocol` | Transport protocol: `tcp` or `udp`. |
| `services[].healthcheck` | Compose healthcheck definition, mapped to a K8s readiness probe. Only present if the service defines a healthcheck. |
| `services[].healthcheck.test` | Healthcheck command. Format: `["CMD-SHELL", "command"]` or `["CMD", "arg1", ...]`. |
| `services[].healthcheck.interval` | Time between probe attempts (e.g. `10s`). Maps to K8s `readinessProbe.periodSeconds`. |
| `services[].healthcheck.timeout` | Max time for a single probe (e.g. `5s`). Maps to K8s `readinessProbe.timeoutSeconds`. |
| `services[].healthcheck.retries` | Consecutive failures before marking unhealthy. Maps to K8s `readinessProbe.failureThreshold`. |
| `services[].healthcheck.start_period` | Grace period before probes count (e.g. `30s`). Maps to K8s `readinessProbe.initialDelaySeconds`. |
| `services[].pods[].name` | K8s pod name (auto-generated with random suffix). |
| `services[].pods[].status` | K8s pod phase. Deployment pods: `Running`, `Pending`. Job pods: `Running`, `Pending`, `Succeeded`, `Failed`, `Unknown`. |
| `services[].pods[].ip` | Pod's cluster-internal IP on the K3s overlay network. |

### Common `jq` Recipes

```bash
# Get host port for a specific service
<kappal> inspect | jq '.services[] | select(.name=="web") | .ports[0].host'

# Check if all services are running
<kappal> inspect | jq '[.services[] | .status] | all(. == "running")'

# List pod IPs for a service
<kappal> inspect | jq '.services[] | select(.name=="api") | .pods[].ip'

# Find services that are not fully ready
<kappal> inspect | jq '.services[] | select(.status != "running" and .status != "completed")'

# Get K3s status
<kappal> inspect | jq '.k3s.status'
```

### When to Use `inspect` vs `ps`

| Need | Use |
|---|---|
| Quick human-readable status check | `kappal ps` |
| Machine-readable JSON for scripting | `kappal inspect` |
| Get published host ports | `kappal inspect` (only source of port data in JSON) |
| Check pod IPs or replica counts | `kappal inspect` |
| Verify K3s container is running | `kappal inspect` |

### Integration Pattern

Use `inspect` to dynamically resolve ports before making HTTP requests:

```bash
PORT=$(<kappal> inspect | jq '.services[] | select(.name=="web") | .ports[0].host')
curl http://localhost:$PORT/health
```

---

## 6. Compose Feature Support

### Fully Supported

services, image, build (context + dockerfile + args), ports (TCP/UDP), volumes (named + bind), environment, env_file, secrets, configs, networks, command, entrypoint, deploy.replicas, labels, restart, depends_on (including `service_completed_successfully` and `service_healthy`), healthchecks (mapped to K8s readiness probes), profiles, one-shot services (Jobs)

### Key Behaviors

- **`restart: "no"`** — Services with this setting run as Kubernetes Jobs instead of Deployments. They execute once and stop cleanly (no CrashLoopBackOff). Use for migrations, seeds, setup tasks.
- **`depends_on` with `condition: service_completed_successfully`** — Kappal injects an init container that waits for the dependency Job to complete before starting the dependent service. This works for both Job-to-Job and Job-to-Deployment dependencies.
- **`depends_on` with `condition: service_healthy`** — Kappal injects an init container that waits for the dependency's pod to reach `Ready` status (healthcheck passing). The dependency service must define a `healthcheck`.
- **`healthcheck`** — Compose healthcheck definitions are translated to K8s readiness probes (exec-based). Both `CMD-SHELL` and `CMD` formats are supported. `interval`, `timeout`, `retries`, and `start_period` map to K8s probe parameters.
- **Compatibility checker on `up`** — Kappal analyzes active services before deploy and prints `Compatibility check: ...` notes for high-signal Compose/K8s mismatch risks.
- **Writable bind mounts** — For writable bind mounts, Kappal injects init-time path preparation so non-root workloads can write without compose-side chmod helper services.
- **Failed Job pods** — When K8s retries a failed Job, old failed pods don't block readiness. Only the latest attempt's status matters.
- **Detach mode timeout** — When `-d` is used, readiness timeout is a warning (exit 0), not a fatal error. Use `--timeout <seconds>` to adjust for complex stacks with sequential job chains.
- **`profiles`** — Services with `profiles:` are excluded from `kappal up` by default, matching Docker Compose behavior. Profile activation is not yet supported.

### Not Supported

extends, resource limits (mem/cpu), log drivers, profile activation (`--profile`)

---

## 7. Build Args vs Runtime Environment (Critical)

These are **separate concerns** — never confuse them:

| Directive | When It Applies | Purpose |
|---|---|---|
| `build.args:` | Build time only | Passed as `--build-arg` during `docker build` |
| `environment:` | Runtime only | Injected when the container starts |
| `env_file:` | Runtime only | Loaded into `environment:` at container start |

**NEVER pass runtime environment variables as build args.** If a build fails because it needs a runtime variable like `DATABASE_URL`, the fix belongs in the app's Dockerfile or configuration — not in kappal.

---

## 8. Project Naming

Kappal derives the project name from the compose file's directory: `<sanitised-basename>-<8-char-sha256-hash>`. This makes names **worktree-safe** — two directories with the same basename (e.g. git worktrees, multiple clones) get different project names and never share K3s containers or networks. When running directly on the host, symlinks to the same physical directory produce the same name. In Docker wrapper mode, `KAPPAL_HOST_DIR` should be a resolved path (see Pitfall #6).

Override with `-p <name>` when you need a fixed name (e.g. scripting, CI).

**Important for AI agents:** Do not hard-code project names derived from directory basenames alone. Always use `kappal inspect | jq '.project'` to discover the actual project name at runtime.

---

## 9. Common Pitfalls

1. **Monorepo mount path** — If any `build.context` goes above the compose directory, the `-v` mount must start from the highest needed ancestor. Getting this wrong causes "file not found" during builds.

2. **depends_on conditions** — Kappal supports both `service_completed_successfully` (Job dependencies) and `service_healthy` (healthcheck-based readiness). The dependency service must define a `healthcheck` for `service_healthy` to work.

3. **YAML anchors** — `x-*` extension fields and YAML anchors work fine. They are parsed by compose-go.

4. **Volume persistence** — `kappal down` preserves volume data. Only `kappal down -v` removes volumes. This is the expected behavior — don't use `-v` unless the user wants a clean slate.

5. **Port conflicts** — Kappal uses `--network host`, so published ports bind directly to the host. If a port is already in use, the service will fail to start. Check with `ss -tlnp` or `lsof -i :<port>` before deploying.

6. **`KAPPAL_HOST_DIR` env var** — Required when running kappal via `docker run`. It tells kappal the real host path of the project directory so the project name is derived from the host path (not the container's `/project`). Without it, all projects would get the same name. Always include `-e KAPPAL_HOST_DIR="<project-root>"` in docker run commands. **Important:** `KAPPAL_HOST_DIR` should be a resolved (non-symlinked) path. Symlink resolution only works when kappal runs directly on the host; inside Docker, the caller must pass the canonical path.

7. **Duplicate port/protocol** — If a compose file maps the same container port and protocol twice (e.g. two services both expose `80/tcp`), kappal will return an error instead of silently overwriting.

8. **Premature compose patching** — For third-party projects, do not edit compose files before trying the drop-in path. Run `up`, capture compatibility notes, and inspect runtime state first.
