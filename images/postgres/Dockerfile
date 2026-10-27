# check=skip=InvalidDefaultArgInFrom
ARG GO_IMAGE
ARG BASE_IMAGE
ARG ETCD_IMAGE

FROM --platform=$BUILDPLATFORM ${GO_IMAGE} AS wrapper
ARG TARGETOS
ARG TARGETARCH
ARG WRAPPER_VERSION
WORKDIR /src
COPY go.mod ./
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags "-s -w -X main.version=${WRAPPER_VERSION}" -o /out/databasify ./cmd/databasify

FROM ${ETCD_IMAGE} AS etcd

FROM ${BASE_IMAGE}
ARG RELEASE
ARG WRAPPER_VERSION
ARG PACKAGES
RUN set -eux; \
    apt-get update; \
    apt-get install -y --no-install-recommends $PACKAGES; \
    rm -rf /var/lib/apt/lists/* /usr/local/bin/gosu
COPY --from=etcd /usr/local/bin/etcd /usr/local/bin/etcdctl /usr/local/bin/
RUN set -eux; \
    groupmod -g 64999 postgres; \
    usermod -u 64999 -g 64999 postgres; \
    find / -xdev \( -uid 999 -o -gid 999 \) -exec chown -h 64999:64999 {} +
COPY --from=wrapper /out/databasify /usr/local/bin/databasify
LABEL org.opencontainers.image.source="https://github.com/databasifylabs/engines" \
      io.databasify.engine="postgres" \
      io.databasify.release="${RELEASE}" \
      io.databasify.wrapper="${WRAPPER_VERSION}"
ENV DATABASIFY_ENGINE=postgres \
    DATABASIFY_RELEASE=${RELEASE}
STOPSIGNAL SIGTERM
ENTRYPOINT ["/usr/local/bin/databasify", "serve"]
CMD []
