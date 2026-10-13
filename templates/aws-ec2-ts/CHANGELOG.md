# Changelog

Every user-visible change, newest first. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and versions follow
[Semantic Versioning](https://semver.org/): the `version` in `package.json`.

## [Unreleased]

### Added

- The service, scaffolded by C4 Factory: an HTTP API that stores and returns
  items (`PUT` and `GET /items/{id}`) in a DynamoDB table, on EC2 instances
  behind an internal load balancer.
