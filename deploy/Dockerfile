# Self-hosted HealthBridge relay. Pure Go (no CGO), runs as non-root.
FROM golang:1.24 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=docker
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X github.com/mauriceL99/Healthsync-copy/internal/version.Version=${VERSION}" \
      -o /out/healthbridge-relay ./cmd/healthbridge-relay \
 && mkdir -p /out/data

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/healthbridge-relay /usr/local/bin/healthbridge-relay
COPY --from=build --chown=65532:65532 /out/data /data
ENV HB_RELAY_ADDR=:8787 HB_RELAY_DB=/data/relay.db
EXPOSE 8787
VOLUME ["/data"]
USER 65532:65532
ENTRYPOINT ["/usr/local/bin/healthbridge-relay"]
