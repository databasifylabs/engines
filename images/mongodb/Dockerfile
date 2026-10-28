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

FROM --platform=$BUILDPLATFORM ${GO_IMAGE} AS pbm
ARG TARGETOS
ARG TARGETARCH
ARG PBM_VERSION
ARG PBM_SHA256
WORKDIR /src
RUN set -eux; \
    curl -fsSL -o /tmp/pbm.tar.gz "https://github.com/percona/percona-backup-mongodb/archive/refs/tags/v${PBM_VERSION}.tar.gz"; \
    echo "${PBM_SHA256}  /tmp/pbm.tar.gz" | sha256sum -c -; \
    tar -xzf /tmp/pbm.tar.gz --strip-components=1; \
    for tool in pbm pbm-agent; do \
        CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -mod=vendor -trimpath \
            -ldflags "-s -w -X github.com/percona/percona-backup-mongodb/pbm/version.version=${PBM_VERSION}" \
            -o "/out/$tool" "./cmd/$tool"; \
    done

FROM ${BASE_IMAGE}
ARG RELEASE
ARG WRAPPER_VERSION
ARG PACKAGES
RUN set -eux; \
    apt-get update; \
    apt-get install -y --no-install-recommends $PACKAGES; \
    rm -rf /var/lib/apt/lists/* /usr/local/bin/gosu
COPY --from=pbm /out/pbm /out/pbm-agent /usr/local/bin/
RUN set -eux; \
    groupmod -g 64999 mongodb; \
    usermod -u 64999 -g 64999 mongodb; \
    find / -xdev \( -uid 999 -o -gid 999 \) -exec chown -h 64999:64999 {} +
COPY --from=wrapper /out/databasify /usr/local/bin/databasify
LABEL org.opencontainers.image.source="https://github.com/databasifylabs/engines" \
      io.databasify.engine="mongodb" \
      io.databasify.release="${RELEASE}" \
      io.databasify.wrapper="${WRAPPER_VERSION}"
ENV DATABASIFY_ENGINE=mongodb \
    DATABASIFY_RELEASE=${RELEASE}
STOPSIGNAL SIGTERM
ENTRYPOINT ["/usr/local/bin/databasify", "serve"]
CMD []
