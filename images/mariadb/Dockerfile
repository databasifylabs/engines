# check=skip=InvalidDefaultArgInFrom
ARG GO_IMAGE
ARG BASE_IMAGE

FROM --platform=$BUILDPLATFORM ${GO_IMAGE} AS wrapper
ARG TARGETOS
ARG TARGETARCH
ARG WRAPPER_VERSION
WORKDIR /src
COPY go.mod ./
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags "-s -w -X main.version=${WRAPPER_VERSION}" -o /out/databasify ./cmd/databasify

FROM --platform=$BUILDPLATFORM ${GO_IMAGE} AS rclone
ARG TARGETOS
ARG TARGETARCH
ARG RCLONE_VERSION
WORKDIR /build
RUN set -eux; \
    go mod init rclone-build; \
    go get "github.com/rclone/rclone@v${RCLONE_VERSION}"; \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath \
        -ldflags "-s -w -X github.com/rclone/rclone/fs.Version=v${RCLONE_VERSION}" \
        -o /out/rclone github.com/rclone/rclone

FROM ${BASE_IMAGE}
ARG RELEASE
ARG WRAPPER_VERSION
ARG PACKAGES
RUN set -eux; \
    apt-get update; \
    apt-get install -y --no-install-recommends $PACKAGES; \
    rm -rf /var/lib/apt/lists/* /usr/local/bin/gosu
COPY --from=rclone /out/rclone /usr/local/bin/rclone
RUN set -eux; \
    groupmod -g 64999 mysql; \
    usermod -u 64999 -g 64999 mysql; \
    find / -xdev \( -uid 999 -o -gid 999 \) -exec chown -h 64999:64999 {} +
COPY --from=wrapper /out/databasify /usr/local/bin/databasify
LABEL org.opencontainers.image.source="https://github.com/databasifylabs/engines" \
      io.databasify.engine="mariadb" \
      io.databasify.release="${RELEASE}" \
      io.databasify.wrapper="${WRAPPER_VERSION}"
ENV DATABASIFY_ENGINE=mariadb \
    DATABASIFY_RELEASE=${RELEASE}
STOPSIGNAL SIGTERM
ENTRYPOINT ["/usr/local/bin/databasify", "serve"]
CMD []
