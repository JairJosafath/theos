# The service's image: `cdk deploy` builds it for linux/arm64 and pushes it to
# the stack's ECR repository. `make check` never builds it; synth only stages
# this folder, minus what .dockerignore leaves out, in cdk.out/.

# Build: install the exact packages in package-lock.json and bundle the
# service into one file with esbuild. It runs on the builder's own platform:
# the bundle is plain JavaScript, so an x86 machine builds the arm64 image
# without emulating npm ci.
FROM --platform=$BUILDPLATFORM node:24-alpine AS build
WORKDIR /build
COPY package.json package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY tsconfig.json ./
COPY src/ src/
RUN npm run bundle

# Run: Node and the bundle only, as the unprivileged node user, on a
# read-only root file system.
FROM node:24-alpine
ENV NODE_ENV=production
WORKDIR /app
COPY --from=build /build/dist/ ./
USER node
EXPOSE 8080
CMD ["node", "--enable-source-maps", "main.cjs"]
