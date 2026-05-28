ADDR ?= 127.0.0.1:8080
DB ?= ./wakeplane.dev.db

.PHONY: help console serve test check

help:
	@printf '%s\n' 'Targets:'
	@printf '%s\n' '  make console        Run daemon and serve http://127.0.0.1:8080/console/'
	@printf '%s\n' '  make serve          Alias for console'
	@printf '%s\n' '  make test           Run Go tests'
	@printf '%s\n' '  make check          Run Go, docsgen, and formatting checks'
	@printf '%s\n' ''
	@printf '%s\n' 'Overrides: make console ADDR=127.0.0.1:18080 DB=./tmp/wakeplane.db'

console:
	@echo "Wakeplane console: http://$(ADDR)/console/"
	WAKEPLANE_HTTP_ADDR=$(ADDR) WAKEPLANE_DB_PATH=$(DB) go run ./cmd/wakeplane serve

serve: console

test:
	go test ./...

check:
	go test ./...
	go run ./tools/docsgen --check
	pnpm run check
