# The C4 Factory UI: the built React app plus server.mjs, which serves it and
# proxies GET requests to the Kubernetes API. Build context is ui/:
#   docker build -t c4factory/ui:dev ui

# Pinned by version and digest, so a tag that moves can't change what a release
# builds (#912). Bump both together, from docker buildx imagetools inspect.
ARG NODE_IMAGE=node:24.21.0-alpine3.24@sha256:ebfe2f90462722a7a4de65e91990e97fe0d401c70e0e762c5b53302f905ec1c1

FROM ${NODE_IMAGE} AS build
WORKDIR /src
# The screenshot tooling's browser is not needed to build.
ENV PLAYWRIGHT_SKIP_BROWSER_DOWNLOAD=1
COPY package.json package-lock.json ./
COPY scripts/check-native.mjs scripts/
# npm ci skips a native binding it fails to fetch without failing (#912), and a
# layer without one would be cached for every later build: check in the same
# step, and install once more before giving up.
# --fetch-retries: npm retries a failed download 5 times (#1494).
RUN { npm ci --no-audit --no-fund --fetch-retries=5 && node scripts/check-native.mjs; } || \
    { npm ci --no-audit --no-fund --fetch-retries=5 && node scripts/check-native.mjs; }
COPY . .
# The release this image is; vite.config.ts defines it as __C4_UI_VERSION__.
# release.yml passes it, a local build stays dev.
ARG VERSION=dev
ENV C4_UI_VERSION=${VERSION}
RUN npm run build

FROM ${NODE_IMAGE}
ENV NODE_ENV=production \
    PORT=8080
WORKDIR /app
# server.mjs has no dependencies, so no node_modules in the runtime image.
COPY --from=build /src/dist ./dist
COPY server.mjs health.mjs oidc.mjs ./
# The node user (uid 1000) ships with the official image.
USER 1000:1000
EXPOSE 8080
CMD ["node", "server.mjs"]
