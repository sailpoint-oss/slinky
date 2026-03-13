GO ?= go
PKG := ./...
BIN_DIR ?= bin
BINARY ?= slinky
BIN := $(BIN_DIR)/$(BINARY)

.PHONY: build test clean check action-image action-run

build: $(BIN)

$(BIN):
	@mkdir -p $(BIN_DIR)
	CGO_ENABLED=0 $(GO) build -o $(BIN) ./

test:
	$(GO) test -v $(PKG)

# Convenience: run the headless check against local test files
check: build
	./$(BIN) check "**/*" --json-out results.json --fail-on-failures true

# Build the Docker-based GitHub Action locally
action-image:
	docker build -t slinky-action -f Dockerfile .

# Run the Action container against the current repo
action-run: action-image
	docker run --rm -v "$(PWD):/repo" -w /repo -e GITHUB_STEP_SUMMARY="/tmp/summary.md" slinky-action sh -lc 'INPUT_TARGETS="**/*" INPUT_CONCURRENCY=8 INPUT_TIMEOUT=5 INPUT_JSON_OUT=results.json INPUT_MD_OUT=results.md INPUT_FAIL_ON_FAILURES=true INPUT_COMMENT_PR=false INPUT_STEP_SUMMARY=true /entrypoint.sh'

clean:
	rm -rf $(BIN_DIR) results.json results.md


