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
  --network host \
  ghcr.io/sandys/kappal:latest <command>
```

**IMPORTANT:** Always confirm the detected mount root path with the user before running.

---

## 4. Autonomous Workflow

When the user says "make this run in kappal", "deploy this with kappal", or similar:

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
- **`deploy.replicas`** — note scaling configuration
- **`restart: "no"`** — these services will run as one-shot Jobs (migrations, seeds, etc.)
- **`depends_on` with conditions** — note any `service_completed_successfully` dependencies (Jobs will complete before dependents start)
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
- Any dependency ordering (`depends_on` with `service_completed_successfully`)
- What ports will be exposed
- Any volumes that will be created

### Step 8: Deploy on user approval

On approval:
- If any services have `build:` contexts: `<kappal-docker-run> up --build -d`
- If all services use pre-built images: `<kappal-docker-run> up -d`

### Step 9: Verify

Run `<kappal-docker-run> ps` and report service status to the user.

---

## 5. Command Reference

| Docker Compose Equivalent | Kappal Command | Notes |
|---|---|---|
| `docker compose up -d` | `<kappal> up -d` | Start services detached |
| `docker compose up --build -d` | `<kappal> up --build -d` | Build images + start |
| `docker compose down` | `<kappal> down` | Stop services, preserve volumes |
| `docker compose down -v` | `<kappal> down -v` | Stop + remove volumes |
| `docker compose ps` | `<kappal> ps` | List running services |
| `docker compose logs <svc>` | `<kappal> logs <svc>` | View logs for a service |
| `docker compose logs -f <svc>` | `<kappal> logs --follow <svc>` | Stream logs |
| `docker compose exec <svc> sh` | `<kappal> exec <svc> sh` | Shell into a service |
| `docker compose build` | `<kappal> build` | Build all images |
| `docker compose build <svc>` | `<kappal> build <svc>` | Build a specific service |
| N/A | `<kappal> clean` | Remove kappal workspace + K3s |
| N/A | `<kappal> eject -o tanka/` | Export as standalone Tanka workspace |

### Additional Flags

| Flag | Scope | Description |
|---|---|---|
| `-f <path>` | Global (before command) | Specify compose file path |
| `-p <name>` | Global (before command) | Override project name |
| `ps -o json` | ps | JSON output |
| `logs --tail 50` | logs | Last N lines |
| `exec -it` | exec | Interactive TTY |
| `exec --index 2` | exec | Target specific replica |

---

## 6. Compose Feature Support

### Fully Supported

services, image, build (context + dockerfile + args), ports (TCP/UDP), volumes (named), environment, env_file, secrets, configs, networks, command, entrypoint, deploy.replicas, labels, restart, depends_on (including `service_completed_successfully`), profiles, one-shot services (Jobs)

### Key Behaviors

- **`restart: "no"`** — Services with this setting run as Kubernetes Jobs instead of Deployments. They execute once and stop cleanly (no CrashLoopBackOff). Use for migrations, seeds, setup tasks.
- **`depends_on` with `condition: service_completed_successfully`** — Kappal injects an init container that waits for the dependency Job to complete before starting the dependent service. This works for both Job-to-Job and Job-to-Deployment dependencies.
- **`profiles`** — Services with `profiles:` are excluded from `kappal up` by default, matching Docker Compose behavior. Profile activation is not yet supported.

### Not Supported

extends, resource limits (mem/cpu), healthcheck enforcement, log drivers, profile activation (`--profile`)

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

## 8. Common Pitfalls

1. **Monorepo mount path** — If any `build.context` goes above the compose directory, the `-v` mount must start from the highest needed ancestor. Getting this wrong causes "file not found" during builds.

2. **depends_on conditions** — Kappal fully supports `service_completed_successfully` for Job dependencies. However, `service_healthy` is not yet enforced. For health-based readiness, handle it in the app's entrypoint.

3. **YAML anchors** — `x-*` extension fields and YAML anchors work fine. They are parsed by compose-go.

4. **Volume persistence** — `kappal down` preserves volume data. Only `kappal down -v` removes volumes. This is the expected behavior — don't use `-v` unless the user wants a clean slate.

5. **Port conflicts** — Kappal uses `--network host`, so published ports bind directly to the host. If a port is already in use, the service will fail to start. Check with `ss -tlnp` or `lsof -i :<port>` before deploying.
