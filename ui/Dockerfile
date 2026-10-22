# syntax=docker/dockerfile:1
# The C4 Factory UI: the built React app plus server.mjs, which serves it and
# proxies GET requests to the Kubernetes API, and the Session client c4session
# for linux amd64 and arm64 under /downloads/, which scaffolded devcontainers
# fetch from the chart's default sessionBinaryURL (#1151). Build context is the
# repository root (the Go module); ui/Dockerfile.dockerignore trims it:
#   docker build -f ui/Dockerfile -t c4factory/ui:dev .

# Pinned by version and digest, so a tag that moves can't change what a release
# builds (#912). Bump both together, from docker buildx imagetools inspect.
ARG NODE_IMAGE=node:24.21.0-alpine3.24@sha256:ebfe2f90462722a7a4de65e91990e97fe0d401c70e0e762c5b53302f905ec1c1
# The Go image the root Dockerfile pins.
ARG GO_IMAGE=golang:1.26.10@sha256:3745f1aa11822297b79113f5e84ce2bffeb039734571c39855809cfe9e5d7be6

FROM ${NODE_IMAGE} AS build
WORKDIR /src
# The screenshot tooling's browser is not needed to build.
ENV PLAYWRIGHT_SKIP_BROWSER_DOWNLOAD=1
COPY ui/package.json ui/package-lock.json ./
COPY ui/scripts/check-native.mjs scripts/
# npm ci skips a native binding it fails to fetch without failing (#912), and a
# layer without one would be cached for every later build: check in the same
# step, and install once more before giving up.
# --fetch-retries: npm retries a failed download 5 times (#1494).
RUN { npm ci --no-audit --no-fund --fetch-retries=5 && node scripts/check-native.mjs; } || \
    { npm ci --no-audit --no-fund --fetch-retries=5 && node scripts/check-native.mjs; }
COPY ui/ .
# The release this image is; vite.config.ts defines it as __C4_UI_VERSION__.
# release.yml passes it, a local build stays dev.
ARG VERSION=dev
ENV C4_UI_VERSION=${VERSION}
RUN npm run build

# The Session client, built as release.yml attaches it to the GitHub release:
# the same flags, one static binary per architecture.
FROM ${GO_IMAGE} AS session
WORKDIR /workspace
COPY go.mod go.sum ./
# go mod download retries (retry: 5 tries, 10 to 40 seconds apart), so one
# slow handshake with the module proxy doesn't fail a release (#1494).
RUN --mount=type=cache,target=/go/pkg/mod \
    retry() { for i in 1 2 3 4 5; do "$@" && return; [ "$i" = 5 ] || sleep $((i * 10)); done; return 1; } \
    && retry go mod download
# ui/Dockerfile.dockerignore lets only the Go sources and ui/ in.
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    for arch in amd64 arm64; do \
      CGO_ENABLED=0 GOOS=linux GOARCH=$arch go build -trimpath -ldflags=-s -o /out/c4session-linux-$arch ./cmd/c4session; \
    done

FROM ${NODE_IMAGE}
ENV NODE_ENV=production \
    PORT=8080
WORKDIR /app
# server.mjs has no dependencies, so no node_modules in the runtime image.
COPY --from=build /src/dist ./dist
COPY --from=session /out/ ./downloads/
COPY ui/server.mjs ui/health.mjs ui/oidc.mjs ./
# The node user (uid 1000) ships with the official image.
USER 1000:1000
EXPOSE 8080
CMD ["node", "server.mjs"]
