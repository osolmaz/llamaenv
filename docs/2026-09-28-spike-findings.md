---
date: 2026-09-28
author: Onur Solmaz <2453968+osolmaz@users.noreply.github.com>
title: llamaenv spike findings
tags: [llamaenv, llama-cpp, spike]
---

# llamaenv spike findings

Answers to the spike questions in the
[implementation plan](2026-09-28-implementation-plan.md), from
`ggml-org/Llama-Windows` at commit `88e14e8` (v0.11.0) and from tests with
Prism's router (`prism-b10687-5d80cff`, CUDA, Linux arm64).

## 1. How the Llama app starts the server

`LlamaApp.LlamaCpp/LlamaManager.cs`, `StartServerAsync`:

```
<llama.exe> serve --port 2276 --jinja [--sleep-idle-seconds N] [--models-preset <app ini>]
```

- `llama.exe` is looked up on PATH at every start: process PATH, then user
  PATH, then machine PATH, first existing file wins. The path is not stored.
- Environment: `HF_HUB_CACHE` (the app's cache folder) and `HF_TOKEN` when set.
- The app's preset INI holds per-model `ctx-size` sections keyed by model ID.
- The official `llama.exe` lives in `%LOCALAPPDATA%\Microsoft\WindowsApps`
  (llama.app's `install.ps1`). A `llama.exe` elsewhere is "external"; the app
  never updates or replaces it.

## 2. Router endpoints the app calls

| Endpoint | Use |
| --- | --- |
| `GET /health` | liveness |
| `GET /models` | model list: `data[]` with `id`, `path`, `status.value`, `architecture`, `source`, `can_remove` |
| `GET /models?reload=1` | reload the preset after a context change |
| `DELETE /models?model=<id>` | delete a cached model |
| `POST /models/load` | `{"model": id}`, optional `ctx_size`; downloads if needed, then loads |
| `POST /models/unload` | `{"model": id}` |
| `GET /models/sse` | live events |
| `POST /v1/chat/completions` | chat, streamed |

The web page uses the same router API plus the OpenAI endpoints.

Model IDs are `<repo>:<quant>`, for example
`prism-ml/Ternary-Bonsai-2-27B-gguf:PQ2_0`. A router lists every GGUF model
in the Hugging Face cache (`"source": "cache"`).

Event stream format: one line per event,
`data: {"model": <id>, "event": "model_status" | "status_change" | ..., "data": {...}}`.
Every event names its model, so two routers' streams can be merged line by
line.

## 3. How the app notices a stopped server

A supervisor polls `/models` every 500 ms and `/health` when that fails. The
process handle is only logged. A stopped shim therefore looks like any stopped
server.

## 4. Version

The app runs `llama --version` and shows the text. It fails open.

## 5. Prism's router has the same API

Prism's fork (`prism-b10743-adfffbe`) has the same router endpoints as
upstream: `/health`, `/models`, `/models/load`, `/models/unload`,
`/models/sse`. Tested with `prism-b10687`: it lists the cache, loads Bonsai 2
27B from `--hf-repo`, reports load progress on `/models/sse`, and unloads.

Consequence for the plan: a custom runtime also runs as a router. llamaenv does
not need to imitate the router API. It routes requests by model, and merges
`/models` and `/models/sse` from the runtimes.

## 6. Windows test machine

- Clean on the Windows side: no Llama app, no `llama.exe`, no Prism build.
- NVIDIA RTX 3080 Laptop GPU, 16 GB.
- Prism's Windows CUDA build needs its `cudart` zip copied next to
  `llama-server.exe`, and the VC++ runtime 14.40 or newer (from the ReLlama
  notes).
