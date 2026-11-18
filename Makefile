# Checks for the modules that follow the lint rules (.golangci.yml). Run from Git Bash on Windows.
# Add a module to LINTED_MODULES once it is clean under the linter.
LINTED_MODULES := services/UserService

.PHONY: fmt fmt-check lint unit integration generate

fmt: ## Format the code
	@for m in $(LINTED_MODULES); do (cd $$m && golangci-lint fmt) || exit 1; done

fmt-check: ## Fail if the code is not formatted
	@for m in $(LINTED_MODULES); do (cd $$m && golangci-lint fmt --diff) || exit 1; done

lint: ## Run the linter
	@for m in $(LINTED_MODULES); do (cd $$m && golangci-lint run --timeout=5m --build-tags integration) || exit 1; done

unit: ## Unit tests with the race detector (no Docker)
	@for m in $(LINTED_MODULES); do (cd $$m && go test -race ./...) || exit 1; done

integration: ## Repository tests on a real PostgreSQL (needs Docker)
	@for m in $(LINTED_MODULES); do (cd $$m && TESTCONTAINERS_RYUK_DISABLED=true go test -race -count=1 -tags integration ./...) || exit 1; done

generate: ## Regenerate wire code and mocks
	cd services/UserService/cmd && wire
	cd services/UserService && go generate ./...
