#!/usr/bin/env bash
set -euo pipefail

for line in 84 97; do
    printf '%s\n' \
        "[percona-pxb-$line-lts]" \
        "name=Percona XtraBackup ${line:0:1}.${line:1} LTS" \
        "baseurl=https://repo.percona.com/pxb-$line-lts/yum/release/9/RPMS/\$basearch" \
        'enabled=1' \
        'gpgcheck=1' \
        > "/etc/yum.repos.d/percona-pxb-$line-lts.repo"
done
rpm --import https://repo.percona.com/yum/PERCONA-PACKAGING-KEY
rpm -q gpg-pubkey-8507efa5
