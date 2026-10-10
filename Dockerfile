FROM --platform=$BUILDPLATFORM node:24-alpine AS ui
WORKDIR /build/web
RUN corepack enable
COPY web/package.json web/pnpm-lock.yaml web/pnpm-workspace.yaml ./
RUN corepack pnpm install --frozen-lockfile
COPY web/ ./
RUN corepack pnpm build

FROM --platform=$BUILDPLATFORM golang:1.27.2-alpine AS build
WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=ui /build/web/dist ./web/dist
ARG TARGETOS
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w" -o /segmentation-manager ./cmd/segmentation-manager

FROM alpine:3.23
LABEL org.opencontainers.image.title="Segmentation Manager" \
      org.opencontainers.image.description="VRF and BGP route-target management for VyOS"
RUN apk add --no-cache ca-certificates && addgroup -g 10001 segmentation-manager && adduser -D -H -u 10001 -G segmentation-manager segmentation-manager && mkdir /data && chown segmentation-manager:segmentation-manager /data
COPY --from=build /segmentation-manager /usr/local/bin/segmentation-manager
USER 10001:10001
VOLUME ["/data"]
EXPOSE 8443
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s CMD wget -q --no-check-certificate -O /dev/null https://127.0.0.1:8443/healthz || exit 1
ENTRYPOINT ["/usr/local/bin/segmentation-manager"]
