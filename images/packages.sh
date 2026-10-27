#!/usr/bin/env bash
set -euo pipefail

engine=$1
shift
repositories="$(dirname "$0")/$engine/repositories.sh"

escape() {
    sed 's/[][\.*^$+?(){}|/]/\\&/g' <<< "$1"
}

apt_candidate_of() {
    apt-cache policy "$1" | awk '/Candidate:/ { print $2 }'
}

rpm_newest_of() {
    local versions
    versions=$(microdnf repoquery "$1" 2> /dev/null \
        | sed -nE "s/^$(escape "$1")-([^-]+-[^-]+)\.($(uname -m)|noarch)\$/\1/p" \
        | sort -u | xargs)
    rpm --eval "%{lua: local newest; for version in string.gmatch('$versions', '%S+') do if not newest or rpm.vercmp(version, newest) > 0 then newest = version end end; print(newest or '')}"
}

[ ! -f "$repositories" ] || bash "$repositories" > /dev/null

if command -v apt-get > /dev/null; then
    apt-get update > /dev/null
    newest_of=apt_candidate_of
else
    newest_of=rpm_newest_of
fi

for package; do
    printf '%s=%s\n' "$package" "$($newest_of "$package")"
done
