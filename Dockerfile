# syntax=docker/dockerfile:1
# Runtime image: COPY the pre-built polypus binary for TARGETARCH (amd64 or arm64) from
# GitHub Release assets, unpacked under docker-bin/<arch>/ (see docker-release.yml).

FROM debian:bookworm-slim

ARG TARGETARCH
ARG APP_VERSION=
ARG GIT_REVISION=
ARG BUILD_CREATED=
ARG REBUILD_REVISION=
ARG BASE_IMAGE=
ARG BASE_DIGEST=

LABEL org.opencontainers.image.title="polypus" \
      org.opencontainers.image.description="Polypus OpenAI-compatible gateway" \
      org.opencontainers.image.url="https://github.com/behaviorengineering/polypus" \
      org.opencontainers.image.source="https://github.com/behaviorengineering/polypus" \
      org.opencontainers.image.documentation="https://github.com/behaviorengineering/polypus#readme" \
      org.opencontainers.image.version="${APP_VERSION}" \
      org.opencontainers.image.revision="${GIT_REVISION}" \
      org.opencontainers.image.created="${BUILD_CREATED}" \
      org.opencontainers.image.vendor="behaviorengineering" \
      org.opencontainers.image.licenses="Apache-2.0" \
      org.opencontainers.image.base.name="${BASE_IMAGE}" \
      org.opencontainers.image.base.digest="${BASE_DIGEST}" \
      com.behaviorengineering.image.rebuild-revision="${REBUILD_REVISION}"

RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates curl \
    && rm -rf /var/lib/apt/lists/*

COPY docker-bin/${TARGETARCH}/polypus /usr/local/bin/polypus
COPY LICENSE /usr/share/doc/polypus/LICENSE
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
