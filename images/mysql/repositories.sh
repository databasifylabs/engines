#!/usr/bin/env bash
set -euo pipefail

printf '%s\n' \
    '[percona-pxb-84-lts]' \
    'name=Percona XtraBackup 8.4 LTS' \
    'baseurl=https://repo.percona.com/pxb-84-lts/yum/release/9/RPMS/$basearch' \
    'enabled=1' \
    'gpgcheck=1' \
    > /etc/yum.repos.d/percona-pxb-84-lts.repo
rpm --import https://repo.percona.com/yum/PERCONA-PACKAGING-KEY
rpm -q gpg-pubkey-8507efa5
