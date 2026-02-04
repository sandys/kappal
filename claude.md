# Claude Code Instructions for Kappal

## Development Guidelines

### 1. Container Lifecycle Handling
- K3s container management must handle ALL states: running, stopped, non-existent
- Never rely on one-off manual cleanup commands - the code must be self-healing
- Always use `docker rm -f` before `docker run` to handle stopped containers

### 2. Kubernetes Resource Naming
- All resource names MUST be sanitized for Kubernetes (no underscores, lowercase only)
- Add validation tests for name sanitization
- Test both the sanitization function AND that manifests use sanitized names

### 3. Secret/Config Handling
- Secrets and ConfigMaps must include actual file contents, not empty data
- Secret data must be base64 encoded
- Config data can be plain text in YAML multiline format
- Mount paths must not be duplicated (check if path already has prefix)

### 4. Testing
- All tests must have hard timeouts (max 90 seconds per operation)
- Run individual tests first before full suite
- Clean up resources programmatically, not manually

### 5. Code Quality
- Run `go vet` and `go fmt` before committing
- Add unit tests for all transformer functions
- Test edge cases: empty files, special characters in names, missing files

## Common Mistakes to Avoid

1. **Don't assume container state** - Always check AND handle all possible states
2. **Don't hardcode paths** - Use filepath.Join and handle absolute/relative paths
3. **Don't duplicate path prefixes** - Check if prefix exists before adding
4. **Don't create empty K8s resources** - Always populate data fields
5. **Don't use `docker exec kubectl`** - Use client-go for all K8s operations (except verification in tests)

## Lint Checks Required

Before any PR, ensure:
```bash
make lint      # golangci-lint
make test      # unit tests including name validation
make fmt       # go fmt
```

## Test Commands

```bash
# Build
make build

# Run single conformance test (use timeout)
timeout 90 docker run --rm -v /var/run/docker.sock:/var/run/docker.sock \
  -v $PWD/testdata/simple:/project -w /project --network host kappal:latest up -d

# Full conformance (with cleanup built into kappal)
make conformance
```
