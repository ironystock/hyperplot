# hyperplot developer tasks. Every target is a thin wrapper around a plain
# go/node command, so you can always run the command directly instead.
# Requires a POSIX shell (Git Bash works on Windows).

GO        ?= go
# The Python example needs only the standard library (Python 3.10+). On
# Windows, where the interpreter is often just `python`: make PYTHON=python
PYTHON    ?= python3
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
test: ## Run Go, client, Python, Node and checker tests, then the conformance checks.
	$(GO) test $(PKGS)
	$(MAKE) --no-print-directory client
	$(MAKE) --no-print-directory py
	$(MAKE) --no-print-directory nodeex
	$(MAKE) --no-print-directory conformance

# The Python and Node example servers and the checker are not Go packages,
# so go test ./... skips them. Their tests compare against files the Go
# side owns (docs/spec.md Appendix B, testdata/conformance/, client/), so a
# change there can break them: run them with every test run.
# docs/wiring_test.go fails if a suite stops being run here or in CI.
.PHONY: py
py: ## Run the Python tests (unittest in examples/python and tools/hpcheck).
	$(PYTHON) -m unittest discover examples/python
	$(PYTHON) -m unittest discover tools/hpcheck

# Like the client, node --test must run inside the directory.
.PHONY: nodeex
nodeex: ## Run the Node example tests (node --test in examples/node).
	cd examples/node && node --test

# The checker (tools/hpcheck) against what the example tests cannot see:
# the files the Python and Node fixture renderers write, the gallery's
# static export (every chart kind, checked with the universal rules), and
# the dashboard, Python and Node servers over HTTP, each started on a free port by
# --spawn and stopped afterwards. The dashboard is built rather than run
# with go run, so stopping it stops the server and not only go run. The
# output goes to a scratch directory that is removed afterwards; pwd -W
# turns it into a path the Windows Python can start a program from.
HPCHECK   = $(PYTHON) tools/hpcheck/hpcheck.py
HPREQUIRE = line-time,empty,bars-band,escaping,collisions

.PHONY: conformance
conformance: ## Check the Python and Node fixture output, the gallery export and the dashboard, Python and Node servers with hpcheck.
	$(HPCHECK) fixtures
	@set -e; out="$$(mktemp -d)"; trap 'rm -rf "$$out"' EXIT; \
	out="$$(cd "$$out" && { pwd -W 2>/dev/null || pwd; })"; \
	set -x; \
	$(PYTHON) examples/python/render_fixtures.py "$$out/python"; \
	$(HPCHECK) fixtures --out "$$out/python" --require $(HPREQUIRE); \
	node examples/node/render-fixtures.mjs "$$out/node"; \
	$(HPCHECK) fixtures --out "$$out/node" --require $(HPREQUIRE); \
	$(GO) run ./examples/gallery -out "$$out/gallery"; \
	$(HPCHECK) file "$$out/gallery/index.html" "$$out/gallery/light.html" "$$out/gallery/dark.html"; \
	$(GO) build -o "$$out/dashboard.exe" ./examples/dashboard; \
	$(HPCHECK) server http://127.0.0.1:8080 --config tools/hpcheck/targets/dashboard.json \
		--spawn "$$out/dashboard.exe -addr 127.0.0.1:{port}"; \
	$(HPCHECK) server http://127.0.0.1:8080 --config examples/python/hpcheck.json \
		--spawn "$(PYTHON) examples/python/server.py --addr 127.0.0.1:{port}"; \
	$(HPCHECK) server http://127.0.0.1:8080 --config examples/node/hpcheck.json \
		--spawn "node examples/node/server.mjs --addr 127.0.0.1:{port}"

# node --test must run inside client/: from the repository root it finds
# no test files and passes without running the client suite.
.PHONY: client
client: ## Run the client tests (node --test in client/).
	cd client && node --test

# The real-browser tests build and run the Go examples, start the Python
# and Node examples (with $(PYTHON) and node), and drive an installed
# Chrome (CHROME_PATH) through playwright-core, a dev-only dependency: run
# `npm ci` in client/ first. client/browser/examples.browser.mjs opens
# every example. Not part of `make test`.
.PHONY: browser
browser: ## Run the real-browser tests, every example included (needs Chrome, and npm ci in client/).
	cd client && PYTHON=$(PYTHON) npm run test:browser

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

# The examples' preview/ SVGs (the pictures in the recipe READMEs) come
# from the same renderer, so a renderer change rewrites them too. Only the
# packages that have a preview/ directory define -update, so they are
# listed rather than passed as ./examples/...: go test fails a package
# that does not know the flag.
PREVIEW_PKGS = $(patsubst %/preview/,./%,$(wildcard examples/*/preview/ examples/*/*/preview/))

.PHONY: golden
golden: ## Regenerate golden SVG files in testdata/ and the examples' preview SVGs, then review the diff.
	$(GO) test . -run Golden -update
	$(GO) test $(PREVIEW_PKGS) -run Preview -update
	@echo "Golden files updated. Review them with: git diff --text testdata/ examples/"

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

# check mirrors the CI jobs on one machine and one Go version, including
# the real-browser tests (so it needs Chrome and `npm ci` in client/). CI
# also runs the tests on Linux, Windows and macOS with Go 1.24.x and
# stable, the client and Node example tests on two Node versions, the
# Python example and checker tests on two Python versions, and a fuzz
# smoke test (make fuzz).
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
	$(MAKE) --no-print-directory py
	$(MAKE) --no-print-directory nodeex
	$(MAKE) --no-print-directory conformance
	$(MAKE) --no-print-directory browser
