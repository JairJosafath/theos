# Changelog

Every user-visible change, newest first. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and versions follow
[Semantic Versioning](https://semver.org/): the `version` in `package.json`.

## [Unreleased]

### Added

- The event consumer, scaffolded by theos: an EventBridge rule on the
  default event bus that sends each `ItemSubmitted` event of this Container
  to a Lambda function, which stores the item it carries in a DynamoDB table
  once per event id, retrying an event that fails and moving it to a
  dead-letter queue after the last try, with alarms and a dashboard, and in
  production backups and tracing.
