# Debian versions must start with a digit: tags look like v0.1.0; untagged trees get 0.0.0+git<hash>.
VERSION ?= $(shell v=$$(git describe --tags --dirty 2>/dev/null | sed 's/^v//'); [ -n "$$v" ] && echo "$$v" || echo "0.0.0+git$$(git rev-parse --short HEAD 2>/dev/null)")

BUILD_DATE ?= $(shell date -u +%Y-%m-%d)

.PHONY: fmt test-go test-web test build deb deploy-bundle clean

fmt:
	gofmt -l -w . && go mod tidy

test-go:
	go vet ./... && go test ./... -count=1 -race

test-web:
	cd web && npm test -- --run

test: test-go test-web

build:
	CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=$(VERSION)" -o bin/kunveno-server ./cmd/kunveno-server
	CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=$(VERSION)" -o bin/kunveno-echo ./cmd/kunveno-echo
	cd web && VITE_APP_VERSION=$(VERSION) VITE_BUILD_DATE=$(BUILD_DATE) npm run build

deb: build
	mkdir -p dist
	VERSION=$(VERSION) nfpm package -f deploy/nfpm/server.yaml -p deb -t dist/
	VERSION=$(VERSION) nfpm package -f deploy/nfpm/web.yaml -p deb -t dist/

# Files the host needs next to the images CI publishes to GHCR: compose file and configuration
# templates only. No binaries and no client bundle travel to the host.
deploy-bundle:
	rm -rf dist/kunveno-docker && mkdir -p dist/kunveno-docker
	cp deploy/docker/docker-compose.yml deploy/docker/prometheus.yml.template dist/kunveno-docker/
	cp deploy/docker/env.example deploy/config.example.yaml dist/kunveno-docker/
	cp -r deploy/docker/grafana dist/kunveno-docker/grafana

clean:
	rm -rf bin dist web/dist
