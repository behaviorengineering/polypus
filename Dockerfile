# syntax=docker/dockerfile:1

FROM --platform=$BUILDPLATFORM golang:1.25-bookworm AS builder

WORKDIR /src

COPY go.mod ./
COPY go.sum* ./
RUN go mod download

COPY . .

ARG TARGETARCH
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux GOARCH=${TARGETARCH} go build \
      -ldflags="-s -w" \
      -o /out/polypus ./cmd/polypus

FROM debian:bookworm-slim

RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates \
    && rm -rf /var/lib/apt/lists/*

COPY --from=builder /out/polypus /usr/local/bin/polypus
COPY docker-entrypoint.sh /usr/local/bin/docker-entrypoint.sh
COPY ports.env /etc/polypus/ports.env
RUN chmod +x /usr/local/bin/docker-entrypoint.sh

WORKDIR /workdir

ENV POLYPUS_HOST=0.0.0.0
ENV POLYPUS_PORT=1320
ENV POLYPUS_BACKEND_URL=http://host.docker.internal:1322
EXPOSE 1320

ENTRYPOINT ["docker-entrypoint.sh"]
