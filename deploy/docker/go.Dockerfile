# syntax=docker/dockerfile:1
# Go services (server, echo, player). Built in CI from the repository root:
#   docker build -f deploy/docker/go.Dockerfile --target server --build-arg APP=kunveno-server .
# Only the final stage is published; it holds the static binary and nothing else.

FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
COPY pkg ./pkg
ARG APP
ARG VERSION=dev
# -trimpath drops build paths, -s -w drops the symbol table and DWARF debug data.
RUN CGO_ENABLED=0 go build -trimpath -buildvcs=false -ldflags "-s -w -X main.version=${VERSION}" -o /out/app ./cmd/${APP}

# Every service runs as user 10001, never as root. /var/lib/kunveno belongs to it, so the stack's
# data volume, which Docker fills from the image when it creates it, does too.
FROM alpine:3.22 AS base
RUN adduser -D -H -u 10001 kunveno && mkdir -p /var/lib/kunveno && chown 10001:10001 /var/lib/kunveno

FROM base AS server
COPY --from=build /out/app /usr/local/bin/kunveno-server
USER 10001:10001
ENTRYPOINT ["/usr/local/bin/kunveno-server", "-config", "/etc/kunveno/config.yaml"]

FROM base AS echo
COPY --from=build /out/app /usr/local/bin/kunveno-echo
USER 10001:10001
ENTRYPOINT ["/usr/local/bin/kunveno-echo"]

# The player transcodes media files once with ffmpeg, so its image carries ffmpeg.
FROM base AS player
RUN apk add --no-cache ffmpeg
COPY --from=build /out/app /usr/local/bin/kunveno-player
USER 10001:10001
ENTRYPOINT ["/usr/local/bin/kunveno-player"]
