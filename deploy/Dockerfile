# HealthBridge relay and agent in one image. Pure Go (no CGO), runs as non-root.
# The default entrypoint is the relay; the agent service in docker-compose.yml
# overrides it with /usr/local/bin/healthbridge.
FROM golang:1.24 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=docker
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X github.com/mauriceL99/Healthsync-copy/internal/version.Version=${VERSION}" \
      -o /out/ ./cmd/healthbridge-relay ./cmd/healthbridge \
 && mkdir -p /out/data

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/healthbridge-relay /out/healthbridge /usr/local/bin/
COPY --from=build --chown=65532:65532 /out/data /data
ENV HB_RELAY_ADDR=:8787 HB_RELAY_DB=/data/relay.db
EXPOSE 8787 8788
VOLUME ["/data"]
USER 65532:65532
ENTRYPOINT ["/usr/local/bin/healthbridge-relay"]
