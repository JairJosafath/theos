# syntax=docker/dockerfile:1
# The seed image: seed.sh plus the templates, run by `make seed` as Job gitea/seed.
# Build it from the repository root; BuildKit then uses Dockerfile.dockerignore
# next to this file instead of the root .dockerignore (which only lets Go in):
#   docker build -f hack/seed/Dockerfile -t theos/seed:dev .
FROM alpine:3.24.2@sha256:294b683cb724975bec92580e1e685676bd4b50bda910ddb8c51d4cabeaec77e6
# apk add retries (#1494): apk itself tries a mirror once.
RUN retry() { for i in 1 2 3 4 5; do "$@" && return; [ "$i" = 5 ] || sleep $((i * 10)); done; return 1; } \
 && retry apk add --no-cache bash ca-certificates curl git jq kubectl openssl
COPY --chmod=0755 hack/seed/seed.sh /usr/local/bin/seed
COPY --chmod=0755 hack/seed/catalog.sh /usr/local/bin/catalog.sh
COPY templates/ /templates/
ENV HOME=/tmp TEMPLATES_DIR=/templates
USER 65532:65532
ENTRYPOINT ["/usr/local/bin/seed"]
