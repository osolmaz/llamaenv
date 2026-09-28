---
date: 2026-09-28
author: Onur Solmaz <2453968+osolmaz@users.noreply.github.com>
title: llamaswitch implementation plan
tags: [llamaswitch, llama-cpp, plan]
---

# llamaswitch implementation plan

This plan implements the [requirements](2026-09-28-requirements.md). Status:
proposed, for review. Nothing is implemented.

## Overview

llamaswitch is one Go program, built as one static binary for Windows, macOS,
and Linux. The same binary has two roles:

- **Shim:** installed as `llama` (`llama.exe` on Windows) in llamaswitch's own
  folder, first on the user PATH.
- **Manager:** run as `llamaswitch` for install, config, and status commands.

```
Llama app (tray)
 └─ llama.exe serve --port 2276 ...      llamaswitch shim, first on PATH
     └─ switcher, listens on :2276
         ├─ official llama serve  :<free port>   router for normal models
         └─ custom runtime        :<free port>   started per mapped model
```

## 1. Shim

- `llama serve ...` starts the switcher with the same arguments.
- Every other command (`llama cli`, `llama --version`, ...) runs the real
  official `llama` with the same arguments, standard streams, and exit code.
- The real `llama` is found on PATH after removing llamaswitch's own folder
  from the search, or in llama.app's known install folder. Never itself.
- If no official `llama` is found, the shim prints a clear error that tells the
  user to run `llamaswitch install`, and exits with a nonzero code.

## 2. Switcher

### Backends

- **Official backend:** the real `llama serve` on a free local port. It gets
  the original arguments, with two changes:
  - the port and host are replaced by the backend's own;
  - `runtime` keys are removed from the preset. The switcher writes the
    resulting preset to its own state folder and passes that path.
- **Runtime backend:** one process per mapped model, started on demand, with
  the runtime's `llama-server` and the model's preset options as flags, on a
  free local port.

### Routing

- Requests with a model (`model` field in the body, or `?model=` in the query)
  go to that model's backend.
- Requests without a model (the web page, `/health`, `/props` without a
  model) go to the official backend.
- Streaming responses are forwarded chunk by chunk with no buffering.
- Only the official backend's host and port face the client. Backends listen
  on `127.0.0.1` only.

### Router API for runtime backends

The Llama app and the web page use the router's model API. A single
`llama-server` of a runtime has no router API, so the switcher answers for it:

- `GET /models`: the official list plus the mapped models, each with a status
  (`loaded`, `unloaded`, `loading`, `failed`) that the switcher tracks.
- `POST /models/load`: start the runtime backend, wait for its health, report
  progress.
- `POST /models/unload`: stop the runtime backend.
- Idle unload: stop a runtime backend after the same idle time as the official
  `--sleep-idle-seconds`.

The exact endpoints and JSON shapes come from the spike (section 7).

### Memory

- One model loaded at a time across all backends by default, like
  `--models-max 1`: loading a mapped model unloads the official backend's
  model, and the reverse.
- A setting raises the limit when memory allows.

### Downloads

- A mapped model's files are downloaded by the official router, which only
  needs to fetch them. The runtime backend then loads the cached file.
- This needs a check in the spike. If the official router refuses to download
  a model that it cannot load, the switcher downloads the files itself into the
  standard Hugging Face cache.

## 3. Lifecycle

- **Windows:** the switcher puts every backend into a job object with
  kill-on-close. When the Llama app stops the shim process, Windows stops all
  backends.
- **macOS and Linux:** backends run in the switcher's process group. On
  SIGTERM or SIGINT the switcher stops them and then exits. A backend also
  exits when its parent dies (Linux `PR_SET_PDEATHSIG`; macOS polls the parent).
- The switcher never writes the Llama app's PID file. The app writes the shim's
  PID itself, as it does for the official `llama`.

## 4. Config

Location:

- Windows: `%LOCALAPPDATA%\llamaswitch\`
- macOS: `~/Library/Application Support/llamaswitch/`
- Linux: `~/.config/llamaswitch/`

Files, all INI like llama.cpp presets:

- `runtimes.ini`: runtime name, source, and version.
  ```ini
  [prism-b10743]
  source = hf://buckets/<owner>/<bucket>@prism-b10743
  ```
- `runtimes.lock`: per-platform SHA-256 of each downloaded runtime, written by
  llamaswitch.
- `models.ini`: local overrides.
  ```ini
  [prism-ml/Ternary-Bonsai-2-27B-gguf]
  runtime = prism-b10743
  ```
- `trust.ini`: runtimes that the user allowed.

Resolution order for a model: `models.ini`, then the model repo's `preset.ini`
on the Hub, then the shared list. The result is cached with its source for
`llamaswitch list`.

The files are read again when a model loads, so edits by hand need no restart.

Runtimes and logs live under the same folder: `runtimes/<name>/<platform>/`
and `logs/`.

## 5. Runtimes

- **Local path:** any folder with a `llama-server` (`llama-server.exe`).
- **Bucket:** llama.app's layout. llamaswitch detects the platform and GPU the
  same way llama.app's `install.sh` and `install.ps1` do, downloads the
  matching build, checks its hash, and unpacks it.
- **First runtime:** Prism's fork. Prism's GitHub releases have Windows CUDA
  x64 and arm64 builds today, but not in llama.app's layout and not for Linux
  arm64 with CUDA. For the first milestone, llamaswitch may use Prism's release
  archives directly, with pinned hashes. A bucket in llama.app's layout,
  built with `ggml-org/llama-install.sh` against Prism's fork, is the later
  source.

## 6. Commands

- `llamaswitch install`: install the official `llama` if missing (llama.app's
  script), install the shim, and add its folder to the front of the user PATH.
  Then restart the Llama app if it runs.
- `llamaswitch uninstall`: stop the switcher and backends, remove the PATH
  entry and the folder, restart the Llama app. Offer to delete model files of
  mapped models.
- `llamaswitch status`: backends, ports, loaded models.
- `llamaswitch list`: every known model, its runtime, and the deciding layer.
- `llamaswitch runtime add <name> <source>` / `runtime remove <name>`.
- `llamaswitch map <repo[:quant]> <runtime>` / `unmap <repo[:quant]>`.
- `llamaswitch trust <runtime>`.

## 7. Spike before the build

Read `ggml-org/Llama-Windows` (`LlamaApp.LlamaCpp/LlamaManager.cs`) and test,
to fix these facts:

1. The exact `llama serve` arguments the app passes, and all environment
   variables it sets.
2. Every router endpoint the app calls, with request and response shapes:
   model list, load, unload, download and its progress, health, props.
3. How the app detects that the server stopped, and what it does then.
4. How the app reads the version, and what it does with it.
5. Whether the official router downloads a model that it cannot load.
6. How Llama for Mac finds `llama`, for macOS support.

The spike ends with a short findings document in `docs/`. It can change this
plan.

## 8. Milestones

1. **Spike** (section 7).
2. **Switcher on Linux** with `llama serve`: fake backends in tests, then the
   official build plus Prism's build with Bonsai on a Linux machine.
3. **Windows:** shim, job objects, PATH install and uninstall, Prism's Windows
   CUDA build; test with the Llama app on the Windows test machine.
4. **Mapping layers:** model repo `preset.ini` and the shared list, with
   trust.
5. **macOS**, after the spike's answer on how the Mac app finds `llama`.

## 9. Tests

- **Unit:** config parsing and precedence, model ID matching, argument
  rewriting, preset rewriting.
- **Integration with fake backends:** small fake servers for the official
  router and a runtime, as in the Bonsai installer's tests. Routing, merged
  model list, load and unload, streaming, one model at a time, and cleanup
  when the switcher is stopped.
- **End to end on Windows** with the Llama app and an NVIDIA GPU:
  1. install llamaswitch; Bonsai and an official model both work and switch;
  2. uninstall; the official model works;
  3. install again, delete the folder by hand; the official model works;
  4. update the official `llama`; both models still work.
- **Gates:** `gofmt`, `go vet`, tests, and Slophammer's Go checks in CI,
  set up when the first code lands.

## 10. Risks

- **The app's router API use can change** in a new app version. The spike
  fixes the current contract; tests with fake backends make a change visible.
- **Prism's fork lags upstream** by hundreds of commits. Its `llama-server`
  must accept the preset options that the app writes; unknown options need
  filtering.
- **Trust:** running a build named by a model repo is a supply-chain risk.
  Pinned hashes and a one-time opt-in are required, not optional.
- **Two builds in memory:** switching between them unloads the other model,
  so the first request after a switch waits for a load.

## 11. Future path

If llama.cpp adds a per-model `runtime` preset key, and `llama` fetches and
trusts runtimes itself, the same `models.ini` and model repo `preset.ini` files
keep working, and llamaswitch can be uninstalled.
