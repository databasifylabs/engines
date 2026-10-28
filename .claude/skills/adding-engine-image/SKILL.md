---
name: adding-engine-image
description: "Add a database engine image, or a new version line of an existing engine, to databasifylabs/engines: the Dockerfile extending the official image, the versions.json entry, bundled tools built from source, the smoke test, the Trivy ignore file and the README, then a local build, smoke and CI-exact scan. Use when the user asks to add an engine such as MongoDB or ClickHouse, a new major such as PostgreSQL 19 or MongoDB 7.0, or a tool inside an engine image (backup agent, migration tool). Do not use for routine pin bumps (the weekly refresh workflow opens those) or for changes to the Go wrapper alone."
license: AGPL-3.0-only
metadata:
  author: databasify
---

# Adding an engine image

This repo builds `ghcr.io/databasifylabs/databasify-<engine>:<release>`: the official image, plus the tools the engine needs for HA, backups and migrations, plus the `databasify` Go wrapper as PID 1. The Databasify app (`../databasify`) pins each database to one exact release digest and drives everything through `databasify step` and `databasify ctl`; the app half has its own skill there (`.claude/skills/adding-database-engine`).

Commit on `main`, but **never push without the user's go-ahead**: a push to `main` runs `.github/workflows/images.yml`, which publishes new digests that every Databasify install can pull.

## Files

| Path | Purpose |
|---|---|
| `images/<engine>/Dockerfile` | One Dockerfile for every line of the engine. Copy the closest one (`images/mongodb` has three tools built from source, `images/valkey` one, `images/postgres` copies binaries from another image). |
| `images/<engine>/smoke/processes.json` | A minimal manifest the smoke test runs through the wrapper (`"type": "oneshot"` for prepare steps, `after`, `restart`, `stopTimeout`). |
| `images/<engine>/smoke/check.sh` | Waits for the engine, asserts `$DATABASIFY_RELEASE`, exercises every bundled tool (`--version` at least), checks UID/GID 64999. It runs against every line, so tolerate fields older releases lack (`… ?? false`). |
| `images/<engine>/.trivyignore` | Only unfixable upstream CVEs, grouped under a comment saying what they are and when to remove them. Try `upgrades` first. |
| `images/<engine>/repositories.sh` | Only for a package from a third-party apt repository the base lacks (`images/mysql`). Used by the Dockerfile (bind mount) and `images/packages.sh`; part of the content hash. |
| `versions.json` | One entry per line under `images` (`images["<engine>-<line>"]`). `registry`, `wrapper` and `go` are shared by every image. |
| `README.md` | The engine in the intro, and its license plus every bundled tool's license under Licenses. |

The CI matrix, tags, refresh and publishing all come from `versions.json`; no workflow or Go change is needed.

## Dockerfile

- Stages: `wrapper` (always), one stage per tool built from source (download, `sha256sum -c`, optional `go get $<NAME>_UPGRADES`, `CGO_ENABLED=0 go build -trimpath`), then `FROM ${BASE_IMAGE}`.
- Final stage: `apt-get install -y --no-install-recommends $PACKAGES`, remove `/usr/local/bin/gosu` when the base ships it, copy the tools and the wrapper.
- Remap the engine user from 999 to 64999: `groupmod -g 64999 <user>; usermod -u 64999 -g 64999 <user>; find / -xdev \( -uid 999 -o -gid 999 \) -exec chown -h 64999:64999 {} +`.
- Labels `io.databasify.{engine,release,wrapper}`; env `DATABASIFY_ENGINE`, `DATABASIFY_RELEASE` and any runtime setting the engine needs on every host (MongoDB: `GLIBC_TUNABLES=glibc.pthread.rseq=1`, because the official `rseq=0` makes mongod 8.x crash or refuse to start on Linux 6.19–7.0.13, SERVER-121912); `STOPSIGNAL SIGTERM`; `ENTRYPOINT ["/usr/local/bin/databasify", "serve"]`.
- Tool contract checked by `images.sh smoke`: `bash ps ss ip jq openssl curl`. Inspect every base (older lines run older OS releases) and add only what is missing:

  ```bash
  docker run --rm --entrypoint bash <base>@<digest> -c 'for t in bash ps ss ip jq openssl curl; do command -v $t >/dev/null || echo "missing $t"; done'
  ```

## versions.json

```json
"<engine>-<line>": {
  "engine": "<engine>",
  "line": "<line>",
  "release": "<exact version, same as the official tag>",
  "base": { "image": "<official>", "tag": "<release><suffix>", "digest": "sha256:…" },
  "packages": { "iproute2": "0", "curl": "0", "ca-certificates": "0" },
  "copies": { "<name>": { "image": "…", "tag": "…", "digest": "…" } },
  "modules": { "<name>": { "module": "github.com/…", "version": "…" } },
  "sources": { "<name>": { "version": "…", "sha256": "…", "upgrades": ["module@version"] } }
}
```

- Set every package to `"0"`, then `images/images.sh refresh <key>` pins packages and moves base, copies and modules to the newest patch of the line. It can also move the shared `go` digest: revert that unless every image should rebuild.
- Base digest: `docker buildx imagetools inspect <image>:<tag> --format '{{json .Manifest}}' | jq -r .digest`.
- Build args (`images/images.sh args <key>`), `<NAME>` = upper-cased key with non-alphanumerics as `_`: `copies` → `<NAME>_IMAGE`; `modules` → `<NAME>_VERSION`; `sources` → `<NAME>_VERSION`, `<NAME>_SHA256`, `<NAME>_UPGRADES`.
- Go tools with a semver module path go in `modules`; anything else (no `/vN`, vendored trees) in `sources` with a sha256 you computed from the tarball yourself. Use `upgrades` to move vendored dependencies past CVEs.
- Check each bundled tool supports the new line. When one lags (PBM 2.17 has no MongoDB 9.0), ship the line anyway and tell the user, so the app can turn that feature off for the line.

## Verify (Docker must be running)

```bash
images/images.sh build <key> --load --tag smoke:<key>
images/images.sh smoke smoke:<key> <engine>
docker run --rm -v /var/run/docker.sock:/var/run/docker.sock -v "$PWD:/repo:ro" "$(sed -n 's/^  TRIVY_IMAGE: //p' .github/workflows/images.yml)" \
  image --scanners vuln --severity CRITICAL,HIGH --ignore-unfixed --exit-code 1 --ignorefile /repo/images/<engine>/.trivyignore smoke:<key>
docker run --rm -v "$PWD:/mnt" -w /mnt koalaman/shellcheck:v0.10.0 -s bash -S warning images/<engine>/smoke/check.sh
```

Smoke every line of the engine when you change its shared Dockerfile or `check.sh`. Your Docker kernel may differ from CI's: a smoke that passes locally can still fail on the runner.

## After the user approves the push

- Watch the run: `gh run watch <id> --exit-status`. A line whose smoke or scan fails is not published, and the other lines are.
- A new engine's GHCR package starts **private**: the user makes it public and grants this repo Write under Manage Actions access.
- Confirm anonymous pulls and report the digests:

  ```bash
  tok=$(curl -s "https://ghcr.io/token?scope=repository:databasifylabs/databasify-<engine>:pull" | jq -r .token)
  curl -sI -H "Authorization: Bearer $tok" -H "Accept: application/vnd.oci.image.index.v1+json" https://ghcr.io/v2/databasifylabs/databasify-<engine>/manifests/<release> | grep -i docker-content-digest
  ```

- Rebuilding a published release (same version, new digest) shows up in Databasify as "Refresh image".
- `.github/workflows/refresh.yml` opens a pull request every Monday with newer pins for every line, so the new entry is kept current from then on.

## Wrapper contract the smoke and the app rely on

- `databasify serve` reads `/srv/<engine>/conf/processes.json`: `processes[]` (`name`, `argv`, `user`, `env`, `after`, `restart` always|on-failure|never, `restartSec`, `stopSignal`, `stopTimeout`, `enabled`, `type` simple|oneshot) and `schedules[]` (`name`, `every` or `onCalendar` "HH:MM" UTC, `argv`, `user`, `timeout`). A process's `env` overrides the image's.
- `databasify ctl` mirrors systemctl (`start`, `stop`, `restart`, `reload`, `enable --now`, `is-active`, `show -p … --value`, `run`, `logs`, `run-schedule`); `databasify step <name>` runs a script from stdin; `databasify info` prints `engine=`, `version=`, `image=`, `wrapper=`.
- Keep the CLI and manifest schema backward compatible: the app refuses images whose `io.databasify.wrapper` is older than its `databasify.images.minimum_wrapper`.
