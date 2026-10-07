FROM golang:1.25-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -o /out/wakeplane ./cmd/wakeplane && \
    CGO_ENABLED=0 go build -trimpath -o /out/wakeplaned ./cmd/wakeplaned && \
    CGO_ENABLED=0 go build -trimpath -o /out/automation-runner ./examples/automation-runner

FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates curl util-linux && \
    rm -rf /var/lib/apt/lists/* && mkdir -p /data && chown 10001:10001 /data
COPY --from=build /out/ /usr/local/bin/
COPY --chmod=755 infra/container/entrypoint.sh /usr/local/bin/wakeplane-entrypoint

USER 10001:10001
WORKDIR /data
ENV WAKEPLANE_DB_PATH=/data/wakeplane.db
ENV CONTAINER_HEALTH_PATH=/readyz
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s \
    CMD curl -fsS "http://127.0.0.1:${PORT:-8080}${CONTAINER_HEALTH_PATH}" || exit 1
ENTRYPOINT ["/usr/local/bin/wakeplane-entrypoint"]

CMD ["/usr/local/bin/wakeplane", "serve"]
