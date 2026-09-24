# Personal Task 1: all checks run without starting services or external model calls.
# Requires make + Go (+ docker compose for compose-config). Without make, run the
# individual commands listed in README.md ("本地静态检查") instead.
GO ?= go
COMPOSE ?= docker compose
COMPOSE_FILE := deploy/compose/docker-compose.yml
COMPOSE_PROFILES := --profile core --profile search --profile vector
# Synthetic value for static validation only; never a real credential.
COMPOSE_STATIC_PASSWORD := synthetic-static-only

.PHONY: check fmt-check test vet compose-config lint

check: fmt-check test vet compose-config

# ./deploy holds the static Compose guard test; keep format coverage aligned
# with the read-only CI workflow.
fmt-check:
	@unformatted="$$(gofmt -l ./cmd ./internal ./deploy)"; \
	if [ -n "$$unformatted" ]; then printf 'Unformatted Go files:\n%s\n' "$$unformatted"; exit 1; fi

test:
	$(GO) test ./...

vet:
	$(GO) vet ./...

# Static parse only: never pulls or starts images (plan §7.7, ADR 0006).
compose-config:
	@command -v docker >/dev/null 2>&1 || { \
		echo "compose-config: docker not found; the Compose file was NOT statically verified in this environment"; \
		exit 1; }
	@MEMX_LOCAL_PG_PASSWORD=$(COMPOSE_STATIC_PASSWORD) $(COMPOSE) $(COMPOSE_PROFILES) -f $(COMPOSE_FILE) config --quiet

# golangci-lint is intentionally not part of `check`: no verifiable version is
# pinned yet, so no pass may be claimed for it (implementation plan §7.7).
lint:
	@command -v golangci-lint >/dev/null 2>&1 || { \
		echo "lint: golangci-lint is not installed; it stays out of 'check' until a reviewed version is pinned"; \
		exit 1; }
	golangci-lint run
