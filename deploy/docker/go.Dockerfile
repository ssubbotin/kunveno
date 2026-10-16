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

FROM alpine:3.22 AS base

# The server and the player write to /var/lib/kunveno, which the compose stack bind-mounts from
# the host: they start as root only to run as the directory's owner (start-as-owner.sh).
FROM base AS server
RUN apk add --no-cache su-exec
COPY --chmod=0755 deploy/docker/start-as-owner.sh /usr/local/bin/start-as-owner
COPY --from=build /out/app /usr/local/bin/kunveno-server
ENTRYPOINT ["/usr/local/bin/start-as-owner"]
CMD ["/usr/local/bin/kunveno-server", "-config", "/etc/kunveno/config.yaml"]

FROM base AS echo
RUN adduser -D -H -u 10001 kunveno
USER kunveno
COPY --from=build /out/app /usr/local/bin/kunveno-echo
ENTRYPOINT ["/usr/local/bin/kunveno-echo"]

# The player transcodes media files once with ffmpeg, so its image carries ffmpeg.
FROM alpine:3.22 AS player
RUN apk add --no-cache ffmpeg su-exec
COPY --chmod=0755 deploy/docker/start-as-owner.sh /usr/local/bin/start-as-owner
COPY --from=build /out/app /usr/local/bin/kunveno-player
ENTRYPOINT ["/usr/local/bin/start-as-owner"]
CMD ["/usr/local/bin/kunveno-player"]
