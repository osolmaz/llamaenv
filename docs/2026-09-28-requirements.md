---
date: 2026-09-28
author: Onur Solmaz <2453968+osolmaz@users.noreply.github.com>
title: llamaswitch requirements
tags: [llamaswitch, llama-cpp, requirements]
---

# llamaswitch requirements

## Problem

Model developers often ship a custom inference build until their model's
support is upstream: a llama.cpp fork, or a custom vLLM or SGLang image. Unified
apps such as llama.app and the Llama apps, LM Studio, or Ollama have no
standard way for a model to say "run me with this build". So every launch comes
with its own install instructions.

Checked on 2026-09-28:

- The llama.cpp router starts every model's child process with its own
  executable (`bin_path` in `tools/server/server-models.cpp`). No preset key
  chooses another program.
- LM Studio keeps several engine builds, but one is selected globally.
- Ollama ships one engine; a Modelfile has no runtime field.
- The nearest precedent is `trust_remote_code` in Transformers: a model repo
  brings its own code until support is upstream, with an explicit opt-in.

First case: Prism ML's Bonsai 2 27B (`prism-ml/Ternary-Bonsai-2-27B-gguf`). Its
PQ2_0 and PTQ1_0 weights load only with Prism's fork
(`PrismML-Eng/llama.cpp`). Upstream support is in progress.

## Goal

Ship llamaswitch next to the regular Llama app so that a model that needs a
custom llama.cpp build runs with it automatically, and nothing else changes.

## Functional requirements

1. **Per-model runtime.** A model that is mapped to a runtime runs with that
   runtime. Every other model runs with the official `llama` build.
2. **Switch on the fly.** Selecting a model in the Llama app, the llama.cpp web
   page, or any OpenAI-compatible client selects its runtime. No restart and no
   manual step.
3. **One address.** Clients see one server with one model list that contains
   the models of every runtime.
4. **Future-compatible config.** The mapping uses a llama.cpp preset key,
   `runtime`, so the same config works if llama.cpp adds the feature later.
5. **Mapping sources,** highest first:
   1. the user's local override;
   2. the model repo's own `preset.ini` on the Hub with a `runtime` key, set
      by the model developer;
   3. a shared list on the Hub for repos whose owner has not added one.
6. **Models are matched by Hub repo and quant,** for example
   `prism-ml/Ternary-Bonsai-2-27B-gguf:PQ2_0`, never by name patterns. This is
   the model ID that the Llama app uses.
7. **Runtimes come from pinned sources:** a local path, or a bucket in
   llama.app's layout (`<version>/<arch>/<os>/<backend>/...`). A downloaded
   runtime is checked against its SHA-256 hash.
8. **Explicit trust.** A runtime named by a model repo or the shared list runs
   only after the user allows it once. A runtime that the user adds is
   trusted.
9. **Visible decisions.** `llamaswitch list` shows every known model, its
   runtime, and the layer that decided it.
10. **Commands:** `install`, `uninstall`, `status`, `list`, `runtime add`,
    `runtime remove`, `map`, `unmap`, `trust`.

## Requirements for the Llama app and official llama.cpp

1. **No disruption.** Models without a runtime mapping behave exactly as with
   the official `llama serve`, including streaming responses, downloads,
   loading, unloading, idle unload, and the web page.
2. **No file changes outside llamaswitch's own folder.** llamaswitch never
   edits the Llama app's settings, preset, or PID file, and never edits or
   replaces the official `llama` files.
3. **Shadow, do not replace.** llamaswitch puts its own `llama` in its own
   folder first on the user PATH. Official updates keep working, because they
   write only their own files.
4. **Transparent lifecycle.** When the Llama app stops the server process, all
   runtimes that llamaswitch started stop too.
5. **Real version.** `llama --version` reports the official build's version.
6. **Official build always present.** `llamaswitch install` makes sure that
   the official `llama` is installed, through llama.app's own install script.

## Removal safety

1. After `llamaswitch uninstall`, the Llama app starts the official server and
   all officially supported models work.
2. The same holds after the llamaswitch folder is deleted by hand.
3. Model files in the shared Hugging Face cache stay. A model that needs a
   custom runtime then fails to load with the official build; every other model
   works. Uninstall offers to delete such model files.

Basis: the Windows Llama app looks up `llama.exe` on PATH at every start,
does not store the path, and skips a PATH entry whose file does not exist
(`LlamaApp.LlamaCpp/LlamaManager.cs`).

## Platforms

1. Windows x64 and arm64 with the Llama app: first target.
2. Linux with `llama serve` from llama.app.
3. macOS with Llama for Mac, after checking how that app finds `llama`: GUI
   apps on macOS often do not see the shell PATH.

## Non-goals

- Changes to llama.cpp or to the Llama apps.
- Running two runtimes for one model at the same time.
- Building runtimes. llamaswitch uses builds that a model developer publishes.
- vLLM and SGLang. The same idea applies, but they are out of scope for now.

## Success criteria

On a Windows machine with the Llama app, an NVIDIA GPU, and llamaswitch:

1. Bonsai 2 27B and an officially supported model both appear in the app. Both
   answer, and each runs on its own build.
2. Switching between them in the app needs no manual step.
3. After uninstalling llamaswitch, the officially supported model still works
   in the app.
