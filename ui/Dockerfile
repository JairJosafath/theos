# The C4 Factory UI: the built React app plus server.mjs, which serves it and
# proxies GET requests to the Kubernetes API. Build context is ui/:
#   docker build -t c4factory/ui:dev ui

ARG NODE_IMAGE=node:24-alpine

FROM ${NODE_IMAGE} AS build
WORKDIR /src
# The screenshot tooling's browser is not needed to build.
ENV PLAYWRIGHT_SKIP_BROWSER_DOWNLOAD=1
COPY package.json package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY . .
RUN npm run build

FROM ${NODE_IMAGE}
ENV NODE_ENV=production \
    PORT=8080
WORKDIR /app
# server.mjs has no dependencies, so no node_modules in the runtime image.
COPY --from=build /src/dist ./dist
COPY server.mjs health.mjs ./
# The node user (uid 1000) ships with the official image.
USER 1000:1000
EXPOSE 8080
CMD ["node", "server.mjs"]
