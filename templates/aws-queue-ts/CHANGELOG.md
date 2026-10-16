# Changelog

Every user-visible change, newest first. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and versions follow
[Semantic Versioning](https://semver.org/): the `version` in `package.json`.

## [Unreleased]

### Added

- The worker, scaffolded by C4 Factory: an SQS queue whose messages, each an
  item, a Lambda function stores in a DynamoDB table, delivering a message
  that fails again alone and moving it to a dead-letter queue after five
  tries, with alarms and a dashboard, and in production backups and tracing.
