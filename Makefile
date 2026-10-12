# Debian versions must start with a digit: tags look like v0.1.0; untagged trees get 0.0.0+git<hash>.
VERSION ?= $(shell v=$$(git describe --tags --dirty 2>/dev/null | sed 's/^v//'); [ -n "$$v" ] && echo "$$v" || echo "0.0.0+git$$(git rev-parse --short HEAD 2>/dev/null)")

.PHONY: fmt test-go test-web test build deb docker-bundle clean

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
	cd web && npm run build

deb: build
	mkdir -p dist
	VERSION=$(VERSION) nfpm package -f deploy/nfpm/server.yaml -p deb -t dist/
	VERSION=$(VERSION) nfpm package -f deploy/nfpm/web.yaml -p deb -t dist/

# Self-contained directory for `docker compose up --build` on the target host: built artifacts only,
# source maps removed (they embed the client sources).
docker-bundle: build
	rm -rf dist/kunveno-docker && mkdir -p dist/kunveno-docker
	cp bin/kunveno-server bin/kunveno-echo deploy/docker/Dockerfile.server deploy/docker/Dockerfile.echo deploy/docker/Dockerfile.web deploy/docker/nginx.conf.template deploy/docker/docker-compose.yml dist/kunveno-docker/
	cp deploy/docker/env.example dist/kunveno-docker/env.example
	cp deploy/docker/prometheus.yml.template dist/kunveno-docker/
	cp -r deploy/docker/grafana dist/kunveno-docker/grafana
	cp deploy/config.example.yaml dist/kunveno-docker/config.example.yaml
	cp -r web/dist dist/kunveno-docker/web
	find dist/kunveno-docker/web -name '*.map' -delete
	sed -i '/^\/\/# sourceMappingURL=/d' dist/kunveno-docker/web/assets/*.js

clean:
	rm -rf bin dist web/dist
