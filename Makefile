.PHONY: build test clean docker-build docker-test conformance

# Build binary in Docker
build:
	docker build -f Dockerfile.build -t kappal-builder .
	docker run --rm -v $(PWD):/output kappal-builder sh -c "cp /usr/local/bin/kappal /output/"

# Run unit tests in Docker
test:
	docker build -f Dockerfile.build -t kappal-builder .
	docker run --rm kappal-builder go test -v ./...

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
