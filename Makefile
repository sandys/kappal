.PHONY: build test clean docker-build docker-test conformance lint-ux lint-compose lint-adhoc lint-k8s lint-volumes lint-exec-docker lint-all

# Build binary in Docker
build:
	docker build -f Dockerfile.build -t kappal-builder .
	docker run --rm --entrypoint sh -v $(PWD):/output kappal-builder -c "cp /usr/local/bin/kappal /output/"

# Run unit tests in Docker
test:
	docker build -f Dockerfile.build -t kappal-builder .
	docker run --rm --entrypoint go kappal-builder test -v ./...

# Build Docker image for kappal
docker-build:
	docker build -f Dockerfile.build -t kappal:latest .

# Run conformance tests
conformance: docker-build
	./scripts/conformance-test.sh

# Run integration tests (requires Docker-in-Docker)
integration: docker-build
	./scripts/integration-test.sh

# Clean up
clean:
	rm -f kappal
	docker rm -f kappal-k3s 2>/dev/null || true
	docker rmi kappal:latest kappal-builder 2>/dev/null || true

# Quick dev iteration - build and test simple compose file
dev: docker-build
	./scripts/dev-test.sh

# Format code
fmt:
	docker run --rm -v $(PWD):/workspace -w /workspace golang:1.22 go fmt ./...

# Lint code
lint:
	docker run --rm -v $(PWD):/workspace -w /workspace golangci/golangci-lint:latest golangci-lint run

# Lint UX - ensure kubectl is not exposed to users
lint-ux:
	@chmod +x scripts/lint-no-kubectl-exposure.sh
	@./scripts/lint-no-kubectl-exposure.sh

# Lint compose feature coverage - ensure all compose-go fields are properly supported
lint-compose:
	@chmod +x scripts/lint-compose-features.sh
	@./scripts/lint-compose-features.sh

# Lint for ad-hoc Docker commands that should be kappal commands
lint-adhoc:
	@chmod +x scripts/lint-no-adhoc-docker.sh
	@./scripts/lint-no-adhoc-docker.sh

# Lint for correct Kubernetes patterns (command/args, Services for DNS, named volumes)
lint-k8s:
	@chmod +x scripts/lint-k8s-patterns.sh
	@./scripts/lint-k8s-patterns.sh

# Lint for volume persistence patterns (down preserves volumes, down -v removes them)
lint-volumes:
	@chmod +x scripts/lint-volume-persistence.sh
	@./scripts/lint-volume-persistence.sh

# Lint for exec.Command("docker", ...) in Go code - use Docker SDK instead
lint-exec-docker:
	@chmod +x scripts/lint-no-exec-docker.sh
	@./scripts/lint-no-exec-docker.sh

# Run all custom lints
lint-all: lint lint-ux lint-compose lint-adhoc lint-k8s lint-volumes lint-exec-docker
