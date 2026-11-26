# hyperplot developer tasks. Every target is a thin wrapper around a plain
# go/node command, so you can always run the command directly instead.
# Requires a POSIX shell (Git Bash works on Windows).

GO        ?= go
PKGS      ?= ./...
FUZZTIME  ?= 30s
BENCHTIME ?= 1s
COVERFILE ?= coverage.out
# Keep in step with STATICCHECK_VERSION in .github/workflows/ci.yml.
STATICCHECK_VERSION := 2026.2.2

.DEFAULT_GOAL := help

.PHONY: help
help: ## Show this help.
	@awk 'BEGIN {FS = ":.*## "} /^[a-zA-Z_-]+:.*## / {printf "  \033[36m%-10s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)

.PHONY: test
test: ## Run Go and client tests.
	$(GO) test $(PKGS)
	$(MAKE) --no-print-directory client

# node --test must run inside client/: from the repository root it finds
# no test files and passes without running the client suite.
.PHONY: client
client: ## Run the client tests (node --test in client/).
	cd client && node --test

.PHONY: race
race: ## Run Go tests with the race detector (needs cgo).
	$(GO) test -race $(PKGS)

.PHONY: vet
vet: ## Run go vet.
	$(GO) vet $(PKGS)

# The same checks as the first steps of the CI lint job. git grep
# --untracked also covers new files that are not committed yet.
.PHONY: hygiene
hygiene: ## Check LF line endings, zero dependencies and a tidy go.mod.
	@cr="$$(printf '\r')"; \
	if git grep -lI --untracked -e "$$cr" -- . ; then \
		echo "CRLF line endings found in the files above."; exit 1; \
	fi
	@deps="$$($(GO) list -m all | tail -n +2)"; \
	if [ -n "$$deps" ]; then \
		echo "hyperplot must depend only on the standard library. Found:"; \
		echo "$$deps"; exit 1; \
	fi
	$(GO) mod tidy -diff

.PHONY: lint
lint: hygiene ## Run hygiene, check formatting, then go vet, staticcheck and golangci-lint.
	@unformatted="$$(gofumpt -l .)"; \
	if [ -n "$$unformatted" ]; then \
		echo "gofumpt: these files need formatting (run 'make fmt'):"; \
		echo "$$unformatted"; exit 1; \
	fi
	$(GO) vet $(PKGS)
	@staticcheck -version | grep -qF "$(STATICCHECK_VERSION)" || \
		echo "warning: CI runs staticcheck $(STATICCHECK_VERSION); install it with: go install honnef.co/go/tools/cmd/staticcheck@$(STATICCHECK_VERSION)"
	staticcheck $(PKGS)
	golangci-lint run

.PHONY: fmt
fmt: ## Format all Go files with gofumpt.
	gofumpt -w .

.PHONY: fuzz
fuzz: ## Run every fuzz target for FUZZTIME each (default 30s).
	@set -e; for pkg in $$($(GO) list $(PKGS)); do \
		for target in $$($(GO) test -list '^Fuzz' $$pkg | grep '^Fuzz' || true); do \
			echo "==> $$pkg $$target"; \
			$(GO) test $$pkg -run '^$$' -fuzz "^$$target$$" -fuzztime $(FUZZTIME); \
		done; \
	done

.PHONY: golden
golden: ## Regenerate golden SVG files in testdata/, then review the diff.
	$(GO) test . -run Golden -update
	@echo "Golden files updated. Review them with: git diff --text testdata/"

.PHONY: bench
bench: ## Run benchmarks with allocation stats.
	$(GO) test -run '^$$' -bench . -benchmem -benchtime $(BENCHTIME) $(PKGS)

.PHONY: cover
cover: ## Run tests with coverage and write cover.html.
	$(GO) test -covermode=atomic -coverprofile=$(COVERFILE) $(PKGS)
	$(GO) tool cover -func=$(COVERFILE) | tail -n 1
	$(GO) tool cover -html=$(COVERFILE) -o cover.html

.PHONY: demo
demo: ## Run the live dashboard example on http://localhost:8080.
	$(GO) run ./examples/dashboard

# check mirrors the CI jobs on one machine and one Go version. CI also
# runs the tests on Linux, Windows and macOS with Go 1.24.x and stable,
# the client tests on two Node versions, and a fuzz smoke test (make fuzz).
.PHONY: check
check: lint ## Run the CI checks locally, except the fuzz smoke test and the OS/Go matrix.
	@before="$$(git status --porcelain --untracked-files=all -- testdata)"; \
	$(GO) test -race $(PKGS) || exit 1; \
	after="$$(git status --porcelain --untracked-files=all -- testdata)"; \
	if [ "$$before" != "$$after" ]; then \
		echo "Tests changed files under testdata/. Run 'make golden', review and commit the result."; \
		exit 1; \
	fi
	$(GO) test -run '^$$' -bench . -benchtime 1x $(PKGS)
	$(MAKE) --no-print-directory client
