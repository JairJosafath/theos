# syntax=docker/dockerfile:1
# The seed image: seed.sh plus the templates, run by `make seed` as Job gitea/seed.
# Build it from the repository root; BuildKit then uses Dockerfile.dockerignore
# next to this file instead of the root .dockerignore (which only lets Go in):
#   docker build -f hack/seed/Dockerfile -t theos/seed:dev .
FROM alpine:3.24.3@sha256:be2656061b536c1883c4034790e21d0a09c5aaaf51968b89368a3c0371ea2c9a
# apk add retries (#1494): apk itself tries a mirror once.
RUN retry() { for i in 1 2 3 4 5; do "$@" && return; [ "$i" = 5 ] || sleep $((i * 10)); done; return 1; } \
 && retry apk add --no-cache bash ca-certificates curl git jq kubectl openssl
COPY --chmod=0755 hack/seed/seed.sh /usr/local/bin/seed
COPY --chmod=0755 hack/seed/catalog.sh /usr/local/bin/catalog.sh
COPY templates/ /templates/
ENV HOME=/tmp TEMPLATES_DIR=/templates
USER 65532:65532
ENTRYPOINT ["/usr/local/bin/seed"]
