# Step 1: build the plugin binary
FROM --platform=$BUILDPLATFORM golang:1.27.3 AS builder

ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev

WORKDIR /app
COPY go.mod go.sum /app/
RUN go mod download

COPY . /app
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
	go build -ldflags="-s -w -X github.com/pganalyze/cnpg-i-pganalyze/pkg/metadata.Version=${VERSION}" \
	-o /app/bin/cnpg-i-pganalyze ./cmd/plugin

# Step 2: the image that actually runs
FROM scratch
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /app/bin/cnpg-i-pganalyze /cnpg-i-pganalyze
USER 10001:10001
ENTRYPOINT ["/cnpg-i-pganalyze"]
