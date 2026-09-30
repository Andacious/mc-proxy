FROM --platform=$BUILDPLATFORM golang:1.23-alpine AS build

ARG TARGETOS
ARG TARGETARCH

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN go test ./...
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w" -o /out/mc-proxy ./cmd/mc-proxy

# Seed the configuration directory so a new Docker volume is owned by the
# non-root runtime user and the default configuration can be written there.
RUN mkdir -p /out/etc/mc-proxy && chown -R 65532:65532 /out/etc

FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=build /out/mc-proxy /mc-proxy
COPY --from=build --chown=65532:65532 /out/etc/mc-proxy /etc/mc-proxy

VOLUME ["/etc/mc-proxy"]
EXPOSE 8080

ENTRYPOINT ["/mc-proxy"]
CMD ["-config", "/etc/mc-proxy/config.yaml"]
