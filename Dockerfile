# SPDX-License-Identifier: GPL-3.0-or-later
# RepoShelf server image (T-5.1). Built in CI only (no local Docker builds).
#
#   docker run -d -p 7373:7373 -v /mnt/user/appdata/reposhelf:/data \
#     -e REPOSHELF_ADMIN_TOKEN=… ghcr.io/fenron-dev/reposhelf
#
# Runner role (builds in containers): additionally mount the Docker socket
#   -v /var/run/docker.sock:/var/run/docker.sock

FROM golang:1.27-alpine AS build
ARG VERSION=dev
ARG COMMIT=
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath \
      -ldflags "-s -w -X github.com/Fenron-dev/reposhelf/internal/buildinfo.Version=${VERSION} -X github.com/Fenron-dev/reposhelf/internal/buildinfo.Commit=${COMMIT}" \
      -o /out/reposhelf ./cmd/reposhelf

FROM alpine:3.22
ARG TARGETARCH
# act runs GitHub workflows locally for the runner role (T-7.2); pinned with checksum.
ARG ACT_VERSION=0.2.97
ARG ACT_SHA256_AMD64=67b4976a56f33ea7870b798912e738ec44eef38e7578ae7b532f2def1c73a5ad
ARG ACT_SHA256_ARM64=44d48e3d4a6409053fdec6fc6518322467d79a2d5be8f72c021b0421e2ac113d
RUN apk add --no-cache git git-lfs openssh-client ca-certificates tzdata docker-cli wget \
 && git lfs install --system \
 && case "$TARGETARCH" in \
      arm64) ACT_ARCH=arm64; ACT_SHA=$ACT_SHA256_ARM64 ;; \
      *) ACT_ARCH=x86_64; ACT_SHA=$ACT_SHA256_AMD64 ;; \
    esac \
 && wget -qO /tmp/act.tgz "https://github.com/nektos/act/releases/download/v${ACT_VERSION}/act_Linux_${ACT_ARCH}.tar.gz" \
 && echo "${ACT_SHA}  /tmp/act.tgz" | sha256sum -c - \
 && tar -xzf /tmp/act.tgz -C /usr/local/bin act \
 && rm /tmp/act.tgz \
 && mkdir -p /data && chown 99:100 /data
COPY --from=build /out/reposhelf /usr/local/bin/reposhelf
ENV REPOSHELF_DATA=/data \
    REPOSHELF_LISTEN=:7373 \
    REPOSHELF_DOCKER=1 \
    HOME=/data/.reposhelf/home
# Unraid default user nobody:users.
USER 99:100
VOLUME ["/data"]
EXPOSE 7373
HEALTHCHECK --interval=60s --timeout=5s CMD wget -qO- http://127.0.0.1:7373/healthz || exit 1
ENTRYPOINT ["reposhelf"]
CMD ["serve"]
