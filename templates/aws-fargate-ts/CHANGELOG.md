# Changelog

Every user-visible change, newest first. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and versions follow
[Semantic Versioning](https://semver.org/): the `version` in `package.json`.

## [Unreleased]

### Changed

- The stack runs a prebuilt image by digest, its `ImageUri` parameter,
  instead of building one from the `Dockerfile` at deploy: synth and
  `make dist` need no Docker, and the task pulls only from the CDK
  bootstrap's ECR repository.

### Added

- The service, scaffolded by theos: an HTTP API that stores and returns
  items (`PUT` and `GET /items/{id}`) in a DynamoDB table, on ECS on Fargate
  behind an Application Load Balancer, with alarms, a dashboard and
  deployments that roll back on them, and in production backups, tracing
  and a NAT gateway per zone.
- Every route but `GET /health` needs a bearer token: an access token of
  the stack's Cognito user pool, issued to its app client. The README says
  how to sign in for one.
