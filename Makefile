.PHONY: all build-gateway build-gpu-exporter test clean vet fmt cover

VERSION := $(shell git rev-parse --short HEAD)
BUILD_TIME := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -ldflags "-X plinth/internal/version.Version=$(VERSION) \
                     -X plinth/internal/version.Commit=$(VERSION) \
                     -X plinth/internal/version.BuildTime=$(BUILD_TIME)"

all: build-gateway build-gpu-exporter

build-gateway:
	go build -o gateway ./cmd/gateway

build-gpu-exporter:
	go build -o gpu-exporter ./cmd/gpu-exporter

build-gateway-versioned:
	go build $(LDFLAGS) -o gateway ./cmd/gateway

build-gpu-exporter-versioned:
	go build $(LDFLAGS) -o gpu-exporter ./cmd/gpu-exporter

build-all-versioned: build-gateway-versioned build-gpu-exporter-versioned

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
	rm -f gateway gpu-exporter coverage.out
