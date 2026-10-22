# Changelog

Every user-visible change, newest first. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and versions follow
[Semantic Versioning](https://semver.org/): the `version` in `package.json`.

## [Unreleased]

### Added

- The service, scaffolded by C4 Factory: an HTTP API that stores and returns
  items (`PUT` and `GET /items/{id}`) in a DynamoDB table, on EC2 instances
  behind an internal load balancer serving HTTPS, with alarms and a
  dashboard, and in production backups.
- Every route but `GET /health` needs a bearer token: an access token of
  the stack's Cognito user pool, issued to its app client. The README says
  how to sign in for one.
