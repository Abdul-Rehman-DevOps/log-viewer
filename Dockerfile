# syntax=docker/dockerfile:1

ARG GO_VERSION=1.25

FROM golang:${GO_VERSION}-alpine AS build
WORKDIR /src
RUN apk add --no-cache git ca-certificates
COPY go.mod go.sum* ./
RUN go mod download 2>/dev/null || true
COPY . .
ENV GOTOOLCHAIN=auto
RUN go mod tidy && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -buildvcs=false -ldflags="-s -w -X main.version=0.4.0" \
    -o /out/log-viewer ./cmd/log-viewer

FROM alpine:3.20
LABEL org.opencontainers.image.title="Log Viewer" \
      org.opencontainers.image.description="Open-source Kubernetes log viewer with admin panel — by Abdul Rehman" \
      org.opencontainers.image.authors="Abdul Rehman" \
      org.opencontainers.image.source="https://github.com/AIVMNetwork/log-viewer"

RUN apk add --no-cache ca-certificates tzdata \
    && adduser -D -u 1000 logviewer

COPY --from=build /out/log-viewer /usr/local/bin/log-viewer
COPY branding/ /etc/log-viewer/branding/

RUN mkdir -p /data \
    && cp /etc/log-viewer/branding/log-viewer-logo.png /etc/log-viewer/branding/logo.png \
    && chown -R logviewer:logviewer /data /etc/log-viewer

USER logviewer
ENV LOG_VIEWER_DATA=/data \
    LOG_VIEWER_LISTEN=:8080 \
    LOG_VIEWER_ASSETS=/etc/log-viewer/branding

WORKDIR /data
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/log-viewer"]
