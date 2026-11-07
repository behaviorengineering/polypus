# syntax=docker/dockerfile:1
# Runtime image: COPY the pre-built polypus binary for TARGETARCH (amd64 or arm64) from
# GitHub Release assets, unpacked under docker-bin/<arch>/ (see docker-release.yml).

# docker-bake.hcl sets the base (GATEWAY_BASE, GATEWAY_BASE_DIGEST); these defaults serve
# a plain docker build. With a digest, the image is built from exactly the image the labels name.
ARG BASE_IMAGE=debian:bookworm-slim
ARG BASE_DIGEST=
FROM ${BASE_IMAGE}${BASE_DIGEST:+@${BASE_DIGEST}}

ARG TARGETARCH
ARG APP_VERSION=
ARG GIT_REVISION=
ARG BUILD_CREATED=
ARG REBUILD_REVISION=
ARG BASE_IMAGE
ARG BASE_DIGEST

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

# Non-root: uid and gid 10001 own the home (XDG state and cache, the batch store), the
# deploy's volume mount points and /workdir (the relative failure dump dir). Volumes an
# older root image created are handed over by the deploy's volume-owner service.
RUN groupadd --system --gid 10001 polypus \
    && useradd --system --uid 10001 --gid polypus --home-dir /var/lib/polypus \
       --no-create-home --shell /usr/sbin/nologin polypus \
    && install -d -o polypus -g polypus -m 0750 /var/lib/polypus \
       /var/lib/polypus/state /var/lib/polypus/switchyard \
       /var/lib/polypus/inference-failures /workdir

WORKDIR /workdir

ENV HOME=/var/lib/polypus
USER 10001:10001

ENV POLYPUS_HOST=0.0.0.0
ENV POLYPUS_PORT=1320
EXPOSE 1320

HEALTHCHECK --interval=5s --timeout=3s --start-period=30s --retries=6 \
  CMD curl -sf http://127.0.0.1:1320/health || exit 1

ENTRYPOINT ["docker-entrypoint.sh"]
