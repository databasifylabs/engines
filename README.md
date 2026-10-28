# Databasify engines

Database engine images for [Databasify](https://github.com/databasifylabs/databasify): the official PostgreSQL, MySQL, MariaDB, MongoDB, Redis and Valkey images, extended with the tools each engine needs for high availability and backups, plus `databasify`, a small Go supervisor that runs as PID 1.

Images are published to `ghcr.io/databasifylabs/databasify-<engine>`.

## Licenses

The images extend the official images and keep each engine's own license: PostgreSQL License, GPLv2 (MySQL, MariaDB), SSPL (MongoDB), RSALv2/SSPLv1/AGPLv3 (Redis) and BSD-3 (Valkey). The MongoDB images also carry Percona Backup for MongoDB (Apache-2.0), built from source.

## Tags

| Tag | Moves | Published |
|---|---|---|
| `<version>-<sha7>` (e.g. `18.6-a1b2c3d`) | never | every build, any branch |
| `<version>` (e.g. `18.6`) | on rebuild | pushes to `main` |
| `<line>` (e.g. `18`) | yes | pushes to `main` |
| `latest` | yes, newest version of the engine | pushes to `main` |

Databasify uses only exact `<version>` tags: it resolves the newest version of a line once when a database is created, stores its digest, and every node of that database pulls the stored digest. A rebuild of the same version shows up as an image refresh. Never delete tagged image versions; databases pin their digests.

## Layout

- `cmd/databasify`, `internal/` — the Go wrapper: `serve` (supervisor and schedules from `processes.json`), `ctl` (systemctl-style process control), `step` (runs a rendered script), `info`.
- `images/<engine>/Dockerfile` — thin extensions of the official images, pinned by digest.
- `images/images.sh` — shared local and CI build, smoke test, tagging.
- `versions.json` — base images, package pins, Go image and wrapper version.

## Local build and smoke test

```bash
images/images.sh keys
images/images.sh build postgres-18 --load --tag smoke:postgres-18
images/images.sh smoke smoke:postgres-18 postgres
```

## Contract with Databasify

The app renders `processes.json` and the step scripts; this repository runs them. Changes to the `databasify` CLI (`ctl` verbs, `step`, `info`, exit codes) or to the `processes.json` schema must stay backward compatible: release the wrapper first, then the app. The app refuses images whose `io.databasify.wrapper` label is older than its configured minimum.
