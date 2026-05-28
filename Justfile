set dotenv-load := true

addr := env_var_or_default("WAKEPLANE_HTTP_ADDR", "127.0.0.1:8080")
db := env_var_or_default("WAKEPLANE_DB_PATH", "./wakeplane.dev.db")

default:
	@just --list

# Run the local daemon and serve http://127.0.0.1:8080/console/.
console addr=addr db=db:
	@echo "Wakeplane console: http://{{addr}}/console/"
	WAKEPLANE_HTTP_ADDR={{addr}} WAKEPLANE_DB_PATH={{db}} go run ./cmd/wakeplane serve

# Alias for console; the daemon serves the API and UI from the same process.
serve addr=addr db=db:
	@just console {{addr}} {{db}}

# Run Go tests.
test:
	go test ./...

# Run generated-doc, Go, and frontend/docs checks.
check:
	go test ./...
	go run ./tools/docsgen --check
	pnpm run check
