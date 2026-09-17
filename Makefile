.PHONY: all build-gateway build-gpu-exporter test clean vet fmt cover

all: build-gateway build-gpu-exporter

build-gateway:
	go build -o gateway ./cmd/gateway

build-gpu-exporter:
	go build -o gpu-exporter ./cmd/gpu-exporter

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
