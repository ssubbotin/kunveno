# syntax=docker/dockerfile:1
# nginx with the built web client. Built in CI from the repository root:
#   docker build -f deploy/docker/web.Dockerfile .
# Only the final stage is published: nginx, the built bundle (no source maps) and the site template.

FROM node:22-alpine AS build
WORKDIR /web
COPY web/package.json web/package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY web/ ./
ARG VERSION=dev
ARG BUILD_DATE=
RUN VITE_APP_VERSION=${VERSION} VITE_BUILD_DATE=${BUILD_DATE} npm run build \
 && if find dist -name '*.map' | grep -q . || grep -rq sourceMappingURL dist; then echo "source maps in the bundle" >&2; exit 1; fi
# Text files are compressed once here, at the best level; nginx serves the .gz copy to browsers that
# accept gzip (gzip_static). Fonts and images are compressed formats already.
RUN find dist -type f \( -name '*.html' -o -name '*.js' -o -name '*.css' -o -name '*.svg' -o -name '*.json' -o -name '*.txt' \) \
      -exec gzip -9 -k {} +

# The official image renders templates/*.template with envsubst on start, substituting only
# variables defined in the environment (nginx's own $vars are kept).
# The slim variant leaves out modules the site does not use (njs, image filter with its codecs, xslt,
# geoip); it keeps TLS, HTTP/2, gzip_static and the template rendering. 1.30 is the stable branch.
FROM nginx:1.30-alpine-slim
RUN rm -f /etc/nginx/conf.d/default.conf && apk add --no-cache openssl
# Issues a self-signed certificate on first start when ./certs is empty.
COPY --chmod=0755 deploy/docker/web-cert.sh /docker-entrypoint.d/05-kunveno-cert.sh
COPY --from=build /web/dist/ /usr/share/nginx/kunveno/
COPY deploy/docker/nginx.conf.template /etc/nginx/templates/kunveno.conf.template
