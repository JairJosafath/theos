# syntax=docker/dockerfile:1
# The Session client image: c4session plus git, for the test Session pod
# (hack/session/pod.yaml). Build it from the repository root:
#   docker build -f hack/session/Dockerfile -t c4factory/c4session:dev .
ARG BASE_IMAGE=golang:1.26
FROM ${BASE_IMAGE} AS builder
WORKDIR /workspace
COPY go.mod go.sum ./
RUN go mod download
# The root .dockerignore lets only Go sources in.
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags=-s -o c4session ./cmd/c4session

FROM alpine:3.24.2
RUN apk add --no-cache git
COPY --from=builder /workspace/c4session /usr/local/bin/c4session
ENV HOME=/tmp
USER 1000:1000
ENTRYPOINT ["/usr/local/bin/c4session"]
