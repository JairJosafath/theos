# syntax=docker/dockerfile:1
# The seed image: seed.sh plus the templates, run by `make seed` as Job gitea/seed.
# Build it from the repository root; BuildKit then uses Dockerfile.dockerignore
# next to this file instead of the root .dockerignore (which only lets Go in):
#   docker build -f hack/seed/Dockerfile -t c4factory/seed:dev .
FROM alpine:3.24.2
RUN apk add --no-cache bash ca-certificates curl git jq kubectl
COPY --chmod=0755 hack/seed/seed.sh /usr/local/bin/seed
COPY templates/ /templates/
ENV HOME=/tmp TEMPLATES_DIR=/templates
USER 65532:65532
ENTRYPOINT ["/usr/local/bin/seed"]
