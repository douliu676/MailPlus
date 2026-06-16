# syntax=docker/dockerfile:1.7
ARG NODE_IMAGE=node:22-alpine
ARG GOLANG_IMAGE=golang:1.26-alpine

FROM --platform=$BUILDPLATFORM ${NODE_IMAGE} AS frontend-builder
WORKDIR /src/qianduan
COPY qianduan/package*.json ./
RUN npm ci
COPY qianduan/ ./
RUN npm run build

FROM --platform=$BUILDPLATFORM ${GOLANG_IMAGE} AS backend-builder
ARG TARGETOS=linux
ARG TARGETARCH
ARG TARGETVARIANT
WORKDIR /src/houduan
RUN apk add --no-cache ca-certificates tzdata
COPY houduan/go.mod houduan/go.sum ./
RUN go mod download
COPY houduan/ ./
RUN set -eux; \
    target_os="${TARGETOS:-linux}"; \
    target_arch="${TARGETARCH:-$(go env GOARCH)}"; \
    if [ "$target_arch" = "arm" ]; then \
      goarm="${TARGETVARIANT#v}"; \
      if [ -z "$goarm" ] || [ "$goarm" = "$TARGETVARIANT" ]; then goarm="7"; fi; \
      CGO_ENABLED=0 GOOS="$target_os" GOARCH="$target_arch" GOARM="$goarm" go build -trimpath -ldflags="-s -w" -o /out/mail-backend .; \
    else \
      CGO_ENABLED=0 GOOS="$target_os" GOARCH="$target_arch" go build -trimpath -ldflags="-s -w" -o /out/mail-backend .; \
    fi

FROM ${NODE_IMAGE}
WORKDIR /app
RUN apk add --no-cache ca-certificates postgresql-client tzdata

COPY --from=backend-builder /out/mail-backend /app/mail-backend
COPY --from=frontend-builder /src/qianduan/dist /app/web
COPY houduan/bin /app/bin
COPY frontend-server.js /app/frontend-server.js
COPY start.sh /app/start.sh

RUN chmod +x /app/start.sh /app/mail-backend \
    && mkdir -p /app/backups \
    && find /app/bin/xray -type f -name 'xray' -exec chmod +x {} \; 2>/dev/null || true

ENV HOST=0.0.0.0
ENV PORT=4400
ENV FRONTEND_HOST=0.0.0.0
ENV FRONTEND_PORT=4399
ENV BACKEND_URL=http://127.0.0.1:4400
ENV GIN_MODE=release
ENV BACKUP_DIR=/app/backups

EXPOSE 4399 4400
CMD ["/app/start.sh"]
