FROM --platform=$BUILDPLATFORM golang:1.23-alpine AS build

ARG TARGETOS
ARG TARGETARCH

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN go test ./...
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w" -o /out/mc-proxy ./cmd/mc-proxy

FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=build /out/mc-proxy /mc-proxy
ENTRYPOINT ["/mc-proxy"]
CMD ["-config", "/etc/mc-proxy/config.yaml"]
