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

Not verified yet: whether a `preset.ini` in a model's own repository, such as
Prism's, applies by itself when that model is loaded as `<repo>:<quant>`. The
docs show only a separate preset repository used as `repo:section`.

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

Earlier runs in WSL with `-c 98304 -ngl 999` and without the image encoder gave
about 25 to 40 tok/s. So the fixed context size, not its length, is what
matters.

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

Settings for a mapped model are a plain llama.cpp router preset (design
principle 5). Until the model developer ships them on the Hub, the setup for
that model saves the preset in llamaenv's folder, because llama.cpp has no
fixed local place for it. llamaenv passes it only to that model's runtime,
combined with the Llama app's preset, whose values win.

For Bonsai 2 27B, planned:

| File | Read by | Contents |
| --- | --- | --- |
| `%LOCALAPPDATA%\llamaenv\models.ini` (Linux: `~/.config/llamaenv/models.ini`) | llamaenv | `[prism-ml/Ternary-Bonsai-2-27B-gguf]` with `runtime = prism` and `preset = presets\prism.ini` |
| `%LOCALAPPDATA%\llamaenv\presets\prism.ini` (Linux: `~/.config/llamaenv/presets/prism.ini`) | llama.cpp, through llamaenv | `[prism-ml/Ternary-Bonsai-2-27B-gguf:PQ2_0]` with `ctx-size = 98304` and `parallel = 1` |

The preset also works on its own: `llama serve --models-preset prism.ini`.
Later it moves to Prism's model repository on the Hub, and `models.ini` keeps
only a pointer to it.

Status on 2026-09-28: the `preset` key and passing the preset on are not
implemented yet.
