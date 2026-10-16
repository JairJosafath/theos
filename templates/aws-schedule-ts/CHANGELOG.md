# Changelog

Every user-visible change, newest first. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and versions follow
[Semantic Versioning](https://semver.org/): the `version` in `package.json`.

## [Unreleased]

### Added

- The scheduled job, scaffolded by C4 Factory: an EventBridge Scheduler
  schedule that runs a Lambda function every day at 03:00 UTC, keeping a
  record of each run in a DynamoDB table once per scheduled time, retrying a
  run that fails and moving it to a dead-letter queue after the last try,
  with alarms and a dashboard, and in production backups and tracing.
