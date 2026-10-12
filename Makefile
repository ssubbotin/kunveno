VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo 0.0.0)

.PHONY: fmt test-go test-web test build deb clean

fmt:
	gofmt -l -w . && go mod tidy

test-go:
	go vet ./... && go test ./... -count=1 -race

test-web:
	cd web && npm test -- --run

test: test-go test-web

build:
	CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=$(VERSION)" -o bin/kunveno-server ./cmd/kunveno-server
	cd web && npm run build

deb: build
	mkdir -p dist
	VERSION=$(VERSION) nfpm package -f deploy/nfpm/server.yaml -p deb -t dist/
	VERSION=$(VERSION) nfpm package -f deploy/nfpm/web.yaml -p deb -t dist/

clean:
	rm -rf bin dist web/dist
