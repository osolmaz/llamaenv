---
date: 2026-09-29
author: Onur Solmaz <2453968+osolmaz@users.noreply.github.com>
title: Releasing llamaenv
tags: [llamaenv, release]
---

# Releasing llamaenv

A release is a tag on `main`. Pushing it runs every CI check, then
`scripts/build-release.sh` builds the archives, and the release workflow
publishes them with `SHA256SUMS` and notes generated from the commits.

```sh
git switch main && git pull --ff-only
git tag v0.1.0
git push origin v0.1.0
```

## Versions

llamaenv follows SemVer and is before 1.0, so its interface is not stable yet:

- patch, such as `v0.1.1`: fixes, and small changes that nobody builds on;
- minor, such as `v0.2.0`: a new command, a new config or state file, or a
  changed one.

Never move or rewrite a published tag. Fix a bad release with a new one.

## Archives

| Archive | Contents |
| --- | --- |
| `llamaenv-<version>-windows-amd64.zip`, `-windows-arm64.zip` | `llamaenv.exe`, `README.md`, `LICENSE` |
| `llamaenv-<version>-linux-amd64.tar.gz`, `-linux-arm64.tar.gz` | `llamaenv`, `README.md`, `LICENSE` |
| `llamaenv-<version>-darwin-arm64.tar.gz`, `-darwin-amd64.tar.gz` | the same, for the macOS Llama app exception |

The binaries are static (`CGO_ENABLED=0`) and report the tag in
`llamaenv version`. Run `scripts/build-release.sh v0.0.0-test dist` to build
the same archives locally.

The Windows binary is not signed. Windows Defender may flag it as a false
positive; report each release to Microsoft through the Defender submission
portal.
