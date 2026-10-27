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

FROM ${BASE_IMAGE} AS binlog
ARG RELEASE
WORKDIR /build
RUN set -eux; \
    arch=$(uname -m); \
    rpm --import /etc/pki/rpm-gpg/RPM-GPG-KEY-mysql; \
    curl -fsSLO "https://repo.mysql.com/yum/mysql-8.4-community/el/9/${arch}/mysql-community-client-${RELEASE}-1.el9.${arch}.rpm"; \
    rpm -K "mysql-community-client-${RELEASE}-1.el9.${arch}.rpm" | grep -q 'digests signatures OK'; \
    microdnf install -y --setopt=install_weak_deps=0 cpio; \
    rpm2cpio "mysql-community-client-${RELEASE}-1.el9.${arch}.rpm" | cpio -idm ./usr/bin/mysqlbinlog; \
    ./usr/bin/mysqlbinlog --version | grep -F "Ver ${RELEASE} "

FROM ${BASE_IMAGE}
ARG RELEASE
ARG WRAPPER_VERSION
ARG PACKAGES
RUN set -eux; \
    printf '%s\n' \
        '[percona-pxb-84-lts]' \
        'name=Percona XtraBackup 8.4 LTS' \
        'baseurl=https://repo.percona.com/pxb-84-lts/yum/release/9/RPMS/$basearch' \
        'enabled=1' \
        'gpgcheck=1' \
        > /etc/yum.repos.d/percona-pxb-84-lts.repo; \
    rpm --import https://repo.percona.com/yum/PERCONA-PACKAGING-KEY; \
    rpm -q gpg-pubkey-8507efa5; \
    microdnf install -y --setopt=install_weak_deps=0 $(printf '%s\n' $PACKAGES | sed 's/=/-/'); \
    microdnf clean all; \
    rm -f /usr/local/bin/gosu
COPY --from=rclone /out/rclone /usr/local/bin/rclone
COPY --from=binlog /build/usr/bin/mysqlbinlog /usr/bin/mysqlbinlog
RUN set -eux; \
    groupmod -g 64999 mysql; \
    usermod -u 64999 -g 64999 mysql; \
    find / -xdev \( -uid 999 -o -gid 999 \) -exec chown -h 64999:64999 {} +
COPY --from=wrapper /out/databasify /usr/local/bin/databasify
LABEL org.opencontainers.image.source="https://github.com/databasifylabs/engines" \
      io.databasify.engine="mysql" \
      io.databasify.release="${RELEASE}" \
      io.databasify.wrapper="${WRAPPER_VERSION}"
ENV DATABASIFY_ENGINE=mysql \
    DATABASIFY_RELEASE=${RELEASE}
STOPSIGNAL SIGTERM
ENTRYPOINT ["/usr/local/bin/databasify", "serve"]
CMD []
