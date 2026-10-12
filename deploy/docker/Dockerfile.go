# syntax=docker/dockerfile:1
# Go services. Built in CI from the repository root:
#   docker build -f deploy/docker/Dockerfile.go --target server --build-arg APP=kunveno-server .
# Only the final stage is published; it holds the static binary and nothing else.

FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
ARG APP
ARG VERSION=dev
# -trimpath drops build paths, -s -w drops the symbol table and DWARF debug data.
RUN CGO_ENABLED=0 go build -trimpath -buildvcs=false -ldflags "-s -w -X main.version=${VERSION}" -o /out/app ./cmd/${APP}

FROM alpine:3.22 AS base
RUN adduser -D -H -u 10001 kunveno
USER kunveno

FROM base AS server
COPY --from=build /out/app /usr/local/bin/kunveno-server
ENTRYPOINT ["/usr/local/bin/kunveno-server", "-config", "/etc/kunveno/config.yaml"]

FROM base AS echo
COPY --from=build /out/app /usr/local/bin/kunveno-echo
ENTRYPOINT ["/usr/local/bin/kunveno-echo"]
