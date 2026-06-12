# Multi-stage build for the LLM Gateway Go backend.
# Final image is distroless — small attack surface, no shell.

# ---- build stage --------------------------------------------------------
FROM golang:1.25-alpine AS build
WORKDIR /src

# Cache deps first
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# Static, stripped, version-annotated binary
ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=linux go build \
    -trimpath \
    -ldflags="-s -w -X main.version=${VERSION}" \
    -o /out/gateway ./cmd/gateway

# ---- runtime stage ------------------------------------------------------
FROM gcr.io/distroless/static-debian12:nonroot

WORKDIR /app
COPY --from=build /out/gateway /app/gateway
COPY config/provider_presets.json /app/config/provider_presets.json

USER nonroot:nonroot
EXPOSE 5001

# Healthcheck via wget (distroless has no curl)
# distroless doesn't ship wget either; rely on orchestrator healthcheck.

ENV PORT=5001

ENTRYPOINT ["/app/gateway"]
