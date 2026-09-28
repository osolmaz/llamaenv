---
date: 2026-09-28
author: Onur Solmaz <2453968+osolmaz@users.noreply.github.com>
title: How llama.cpp is configured, and where llamaenv fits
tags: [llamaenv, llama-cpp, config, presets]
---

# How llama.cpp is configured, and where llamaenv fits

This note records how llama.cpp takes its settings, how the Llama app uses
them, and which files llamaenv adds. Sources: `ggml-org/llama.cpp`
`docs/preset.md` and `tools/server/README.md` (master, 2026-09-28),
`ggml-org/Llama-Windows` v0.11.0 (`LlamaApp.LlamaCpp/LlamaManager.cs`), and
tests on Windows (RTX 3080 Laptop, 16 GB) and Linux (DGX Spark).

## llama.cpp's config layers

A later layer overrides an earlier one:

1. **System config file**, `config.ini`, loaded at start when it exists, by
   every llama.cpp program:
   - system-wide: `/etc/llama.cpp/config.ini`, or
     `%PROGRAMDATA%\llama.cpp\config.ini` on Windows;
   - user: `$XDG_CONFIG_HOME/llama.cpp/config.ini`, by default
     `~/.config/llama.cpp/config.ini`, or `%APPDATA%\llama.cpp\config.ini` on
     Windows.

   Only the `[*]` section and the keys before any section count. **Named
   sections are ignored, so this file cannot hold settings for one model.** An
   option that a program does not support is ignored with a warning.
2. **Environment variables**: every server option has one, for example
   `LLAMA_ARG_N_PARALLEL` for `--parallel` and `LLAMA_ARG_CTX_SIZE` for
   `--ctx-size` (see `llama-server --help`).
3. **Command-line options.**
4. **Model presets**, in router mode only: per-model settings from
   `--models-preset <file>`, or from a preset on the Hub.

Neither `config.ini` exists on a new install. Nothing creates them.

## Presets: settings per model

A preset is an INI file. Each section is one model; its keys are server
option names without the leading dashes, such as `ctx-size`, `parallel`,
`n-gpu-layers`, or `hf`:

```ini
[*]
mmap = 1

[prism-ml/Ternary-Bonsai-2-27B-gguf:PQ2_0]
ctx-size = 98304
parallel = 1
```

- **Router preset:** any file, passed as `llama serve --models-preset <file>`.
  The router applies a model's section to that model's child process. `[*]`
  applies to every model. `GET /models?reload=1` reads the file again.
- **Hub preset:** a `preset.ini` in the root of a Hub repository, often a small
  repository that only points to models with `hf = <repo>`. It is loaded like
  `--models-preset`, for example `llama-server -hf user/repo:<section>`.

**llama.cpp has no fixed local place for per-model settings.** They exist only
as a preset file at a path that someone passes, or on the Hub.

A model repository cannot carry its own `preset.ini`: when a repository has one
in its root, `-hf <repo>` downloads only that file and starts in router mode,
and no model files (`common/download.cpp`, `common/arg.cpp`, master on
2026-09-28). A Hub preset is therefore always a separate preset repository.

`--models-preset` takes one path. A second one replaces the first. The
environment variable `LLAMA_ARG_MODELS_PRESET` sets it too.

## Defaults when nothing is set

- **Slots:** without `--parallel`, the server picks the number itself. It
  picked 4 for Bonsai 2 27B.
- **Context:** without `--ctx-size`, the server fits the context to the free
  memory. It picked 87,040 tokens for Bonsai on the 16 GB laptop, which filled
  the VRAM to 16,045 of 16,384 MiB.
- **Image encoder:** `--hf-repo` also loads the repository's multimodal
  projector when there is one.

On Windows, a model that fills the VRAM this way slows down sharply, because
the desktop also uses VRAM and Windows then pages GPU memory. Measured for
Bonsai 2 27B PQ2_0 with Prism's CUDA build:

| Settings | GPU memory | Decode |
| --- | --- | --- |
| llama.cpp defaults (87K context, 4 slots) | 16,045 MiB of 16,384 | about 7 tok/s |
| `ctx-size = 32768`, `parallel = 1` | 13,720 MiB | 39.7 tok/s |
| `ctx-size = 98304`, `parallel = 1` | 15,997 MiB | 7.5 tok/s |
| `ctx-size = 98304`, `parallel = 1`, `no-mmproj = true` | 15,973 MiB | 7.9 tok/s |
| `ctx-size = 98304`, `parallel = 1`, `cache-type-k`/`cache-type-v = q8_0` | 14,693 MiB | 39.3 tok/s |

What matters is whether everything fits next to the desktop's 3 to 4 GB. The
image encoder is small; the context cache is not. An 8-bit context cache fits a
96K context.

## How the Llama app sets them

- It starts `llama serve --port <port> --jinja`, plus
  `--sleep-idle-seconds <n>` when idle unload is on.
- When the user picks a context length for a model in its details, the app
  writes its own router preset with `ctx-size` for that model and passes
  `--models-preset <file>`. The file lives in the app's data folder,
  `%LOCALAPPDATA%\Packages\<app>\LocalCache\Local\Llama\` for the packaged app.
- The `ctx_size` field that it sends with `POST /models/load` is ignored by the
  router, as the app's own code notes.
- It sets no `parallel`. Every model gets the server's default.

## Other locations

| What | Windows | Linux |
| --- | --- | --- |
| Official `llama` (llama.app) | `%LOCALAPPDATA%\Microsoft\WindowsApps\llama.exe` | `~/.local/bin/llama`, `~/.llama-app/llama` |
| Downloaded models | `%USERPROFILE%\.cache\huggingface\hub\`, or `HF_HUB_CACHE` | `~/.cache/huggingface/hub/`, or `HF_HUB_CACHE` |

## Where llamaenv fits

llamaenv MUST NOT write llama.cpp's `config.ini`. It would change every model,
including the officially supported ones (design principle 3).

Settings for a mapped model are a plain llama.cpp preset (design principle 5).
llama.cpp has no fixed local place for per-model settings, so `llamaenv preset`
copies the preset into llamaenv's folder, unchanged. Only that runtime's router
gets it:

- Without a client preset, the router gets the runtime preset itself.
- With one, such as the Llama app's, the router gets a combined file in
  `state/<port>/<runtime>.preset.ini`: the runtime preset with the client's
  preset over it, key by key, so the client's values win. llamaenv writes it
  again before it passes on a reload.
- The default router gets the client's arguments and preset unchanged.

The files for Bonsai 2 27B, from [`examples/bonsai`](../examples/bonsai):

| File | Read by | Contents |
| --- | --- | --- |
| `runtimes.ini` | llamaenv | `[runtime prism]` with the archive URLs, `models = prism-ml/Ternary-Bonsai-2-27B-gguf`, and `preset = presets/prism.ini` |
| `runtimes.lock` | llamaenv | the SHA-256 of each archive, and of a preset that came from a URL |
| `presets/prism.ini` | llama.cpp, through llamaenv | `[prism-ml/Ternary-Bonsai-2-27B-gguf:PQ2_0]` with `ctx-size = 98304`, `parallel = 1`, and an 8-bit context cache |
| `state/<port>/prism.preset.ini` | llama.cpp, written by llamaenv | the combined preset, while the switcher runs |

The folder is `%LOCALAPPDATA%\llamaenv\` on Windows. On Linux, config files and
presets are in `~/.config/llamaenv/`, and `state/` is in
`~/.local/share/llamaenv/`.

Tested on 2026-09-28 on Linux with Prism's build: with an app preset of
`ctx-size = 16384`, Bonsai loaded with `--ctx-size 16384 --parallel 1`; after the
app preset changed to 24576 and a reload, it loaded with 24576. The official
router got the app preset unchanged.
