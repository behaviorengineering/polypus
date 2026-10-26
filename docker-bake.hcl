# Builds the gateway and Switchyard images together. Both copy pre-built binaries from
# docker-bin/<arch>/, so unpack the release archives there first (docker-release.yml
# does). docker-release.yml sets the variables below through the environment.
#
# Local test build for this machine's arch only:
#   PLATFORMS=linux/arm64 docker buildx bake -f docker-bake.hcl --load
# Name the file: without -f, bake also reads docker-compose.yml.

variable "PLATFORMS" {
  # Comma-separated; docker-release.yml drops linux/arm64 for tags without arm64 archives.
  default = "linux/amd64,linux/arm64"
}

variable "GATEWAY_TAGS" {
  # Comma-separated image refs.
  default = "polypus:dev"
}

variable "SWITCHYARD_TAGS" {
  default = "polypus-switchyard:dev"
}

variable "APP_VERSION" {
  default = ""
}

variable "GIT_REVISION" {
  default = ""
}

variable "BUILD_CREATED" {
  default = ""
}

variable "REBUILD_REVISION" {
  default = ""
}

variable "GATEWAY_BASE" {
  default = "debian:bookworm-slim"
}

variable "GATEWAY_BASE_DIGEST" {
  default = ""
}

variable "SWITCHYARD_BASE" {
  default = "debian:trixie-slim"
}

variable "SWITCHYARD_BASE_DIGEST" {
  default = ""
}

group "default" {
  targets = ["gateway", "switchyard"]
}

# Shared by both images: platforms and the OCI label build args.
target "_release" {
  context   = "."
  platforms = split(",", PLATFORMS)
  args = {
    APP_VERSION      = APP_VERSION
    GIT_REVISION     = GIT_REVISION
    BUILD_CREATED    = BUILD_CREATED
    REBUILD_REVISION = REBUILD_REVISION
  }
}

target "gateway" {
  inherits   = ["_release"]
  dockerfile = "Dockerfile"
  tags       = split(",", GATEWAY_TAGS)
  args = {
    BASE_IMAGE  = GATEWAY_BASE
    BASE_DIGEST = GATEWAY_BASE_DIGEST
  }
}

target "switchyard" {
  inherits   = ["_release"]
  dockerfile = "Dockerfile.switchyard"
  tags       = split(",", SWITCHYARD_TAGS)
  args = {
    BASE_IMAGE  = SWITCHYARD_BASE
    BASE_DIGEST = SWITCHYARD_BASE_DIGEST
  }
}
