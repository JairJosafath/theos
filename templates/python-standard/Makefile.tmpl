# The quality contract of this repository. CI runs `make check` on every pull
# request, or, when the Container's checks are BeforeRelease, `make check-light`
# there and `make check` before each release; agents and people run
# `make check` before every commit. `make help` lists the targets.

# The project's version is the one in pyproject.toml. A release passes its tag,
# which must match: make dist VERSION=v1.2.3
PROJECT_VERSION := $(shell uv version --short 2>/dev/null)
VERSION ?= v$(PROJECT_VERSION)
# Every tool runs from the environment `make deps` synced, never re-resolved.
RUN := uv run --no-sync

.DEFAULT_GOAL := help
.PHONY: help check check-light deps fmt fmt-check lint typecheck test package-check dist release-version clean

help: ## List the targets.
	@awk 'BEGIN {FS = ":.*## "} /^[a-z-]+:.*## / {printf "  %-14s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

check: deps fmt-check lint typecheck test package-check ## Everything CI checks.

check-light: deps fmt-check lint typecheck ## check without the tests, which CI runs on each change with checks BeforeRelease.

deps: ## Install the exact packages in uv.lock, failing when it is stale.
	uv sync --locked

fmt: ## Format and sort imports with ruff.
	$(RUN) ruff check --select I --fix .
	$(RUN) ruff format .

fmt-check: ## Fail when a file is not formatted, showing the diff.
	$(RUN) ruff format --diff .

lint: ## Lint with ruff.toml, failing on any finding.
	$(RUN) ruff check --no-fix .

typecheck: ## Type-check with mypy.ini (strict).
	$(RUN) mypy .

test: ## Run the tests, failing on unknown markers and config.
	$(RUN) pytest --strict-markers --strict-config

package-check: ## Fail when the project does not build.
	@tmp=$$(mktemp -d) && trap 'rm -rf "$$tmp"' EXIT && \
		uv build --quiet --out-dir "$$tmp"

dist: release-version deps ## Build the wheel and sdist into dist/ for a release.
	rm -rf dist
	uv build --out-dir dist

release-version: # Fail when VERSION is not pyproject.toml's version.
	@if [ "$(VERSION)" != "v$(PROJECT_VERSION)" ]; then \
		echo "Tag $(VERSION) does not match version $(PROJECT_VERSION) in pyproject.toml." >&2; \
		echo "Set version = \"$(VERSION:v%=%)\" there (uv version $(VERSION:v%=%)), then tag again." >&2; \
		exit 1; \
	fi

clean: ## Remove build output.
	rm -rf dist build
