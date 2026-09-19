.PHONY: all build-gateway build-gpu-exporter build-provision test clean vet fmt cover

VERSION := $(shell git rev-parse --short HEAD)
BUILD_TIME := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -ldflags "-X plinth/internal/version.Version=$(VERSION) \
                     -X plinth/internal/version.Commit=$(VERSION) \
                     -X plinth/internal/version.BuildTime=$(BUILD_TIME)"

all: build-gateway build-gpu-exporter build-provision

build-gateway:
	go build -o gateway ./cmd/gateway

build-gpu-exporter:
	go build -o gpu-exporter ./cmd/gpu-exporter

build-provision:
	go build -o plinth-provision ./cmd/provision

build-gateway-versioned:
	go build $(LDFLAGS) -o gateway ./cmd/gateway

build-gpu-exporter-versioned:
	go build $(LDFLAGS) -o gpu-exporter ./cmd/gpu-exporter

build-provision-versioned:
	go build $(LDFLAGS) -o plinth-provision ./cmd/provision

build-all-versioned: build-gateway-versioned build-gpu-exporter-versioned build-provision-versioned

test:
	go test ./... -race -count=1

vet:
	go vet ./...

fmt:
	gofmt -l .
	gofmt -w .

cover:
	go test ./... -coverprofile=coverage.out
	go tool cover -func=coverage.out

clean:
	rm -f gateway gpu-exporter plinth-provision coverage.out
