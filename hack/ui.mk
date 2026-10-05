##@ UI

# The read-only UI (ui/ and config/ui). Included from the Makefile; the
# fallbacks below let it run alone (make -f Makefile -f hack/ui.mk ...).
KIND ?= kind
KUBECTL ?= kubectl
KIND_CLUSTER ?= c4factory
CONTAINER_TOOL ?= docker
NPM ?= npm
UI_IMG ?= c4factory/ui:dev
# Local port for ui-forward and ui-serve, and for the kubectl proxy behind ui-dev and ui-serve.
UI_PORT ?= 8080
UI_PROXY_PORT ?= 8001

# Every kubectl call names the Kind context, so these targets never touch
# whatever cluster the current context happens to point at.
UI_KUBECTL = $(KUBECTL) --context kind-$(KIND_CLUSTER)
UI_NAMESPACE = c4-factory-ui

# Starts a kubectl proxy for the recipe and stops it when the recipe ends. If a
# proxy already listens on the port, the new one exits and that one is used.
define with-kubectl-proxy
$(UI_KUBECTL) proxy --port $(UI_PROXY_PORT) >/dev/null 2>&1 & proxy=$$!; \
trap 'kill $$proxy 2>/dev/null || true' EXIT; sleep 1;
endef

ui/node_modules: ui/package.json ui/package-lock.json
	cd ui && $(NPM) ci --no-audit --no-fund
	@touch $@

.PHONY: ui-deps
ui-deps: ui/node_modules ## Install the UI's npm dependencies.

.PHONY: ui-dev
ui-dev: ui/node_modules ## Run the UI with live reload on http://localhost:5173, reading the Kind cluster.
	@$(with-kubectl-proxy) cd ui && K8S_API=http://127.0.0.1:$(UI_PROXY_PORT) $(NPM) run dev

.PHONY: ui-build
ui-build: ui/node_modules ## Typecheck and build the UI into ui/dist.
	cd ui && $(NPM) run build

.PHONY: ui-test
ui-test: ui/node_modules ## Typecheck, lint and test the UI (unit tests and server tests).
	cd ui && $(NPM) run check

.PHONY: ui-serve
ui-serve: ui-build ## Serve ui/dist with ui/server.mjs on http://localhost:8080, reading the Kind cluster.
	@$(with-kubectl-proxy) cd ui && PORT=$(UI_PORT) K8S_API=http://127.0.0.1:$(UI_PROXY_PORT) node server.mjs

.PHONY: ui
ui: ## Build the UI image, load it into Kind and deploy config/ui.
	$(call build-load,$(UI_IMG),ui)
	$(UI_KUBECTL) apply -k config/ui
	$(UI_KUBECTL) -n $(UI_NAMESPACE) set image deployment/c4-factory-ui ui=$(UI_IMG)
	@# Same tag, new image: restart so the Pod picks up what was just loaded.
	$(UI_KUBECTL) -n $(UI_NAMESPACE) rollout restart deployment/c4-factory-ui
	$(UI_KUBECTL) -n $(UI_NAMESPACE) rollout status deployment/c4-factory-ui --timeout=180s

.PHONY: ui-forward
ui-forward: ## Forward the in-cluster UI to http://localhost:8080 (Ctrl-C to stop).
	$(UI_KUBECTL) -n $(UI_NAMESPACE) port-forward svc/c4-factory-ui $(UI_PORT):8080

.PHONY: ui-undeploy
ui-undeploy: ## Remove the UI from the Kind cluster (namespace c4-factory-ui and its ClusterRole).
	$(UI_KUBECTL) delete -k config/ui --ignore-not-found
