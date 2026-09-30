# syntax=docker/dockerfile:1
# Runtime image: COPY pre-built linux/amd64 polypus binary from GitHub Release assets (see docker-release.yml).

FROM debian:bookworm-slim

RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates curl \
    && rm -rf /var/lib/apt/lists/*

COPY docker-bin/polypus /usr/local/bin/polypus
COPY docker-entrypoint.sh /usr/local/bin/docker-entrypoint.sh
COPY ports.env /etc/polypus/ports.env
RUN chmod +x /usr/local/bin/docker-entrypoint.sh /usr/local/bin/polypus

WORKDIR /workdir

ENV POLYPUS_HOST=0.0.0.0
ENV POLYPUS_PORT=1320
EXPOSE 1320

HEALTHCHECK --interval=5s --timeout=3s --start-period=30s --retries=6 \
  CMD curl -sf http://127.0.0.1:1320/health || exit 1

ENTRYPOINT ["docker-entrypoint.sh"]
