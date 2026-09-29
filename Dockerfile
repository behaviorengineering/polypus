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

ENTRYPOINT ["docker-entrypoint.sh"]
