# The quality contract of this repository. CI runs `make check` on every pull
# request; agents and people run it before every commit.

.PHONY: check deps fmt fmt-check analyze test build

check: deps fmt-check analyze test ## Everything CI checks.

deps: ## Fetch packages.
	flutter pub get

fmt: ## Format the code.
	dart format lib test

fmt-check: ## Fail when the code is not formatted.
	dart format --output=none --set-exit-if-changed lib test

analyze: ## Lint and type-check, failing on any finding.
	flutter analyze --fatal-infos --fatal-warnings

test: ## Run the tests.
	flutter test

build: ## Build a debug APK.
	flutter build apk --debug
