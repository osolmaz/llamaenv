---
date: 2026-09-28
author: Onur Solmaz <2453968+osolmaz@users.noreply.github.com>
title: llamaenv requirements
tags: [llamaenv, llama-cpp, requirements]
---

# llamaenv requirements

> **Work in progress.** llamaenv is a stopgap. It may be deprecated, or its
> idea absorbed into llama.cpp itself, for example as a per-model `runtime`
> preset key. When that happens, llamaenv should be removed.

## Problem

Model developers often ship a custom llama.cpp build, usually a fork, until
their model's support is upstream. llama.app and the Llama apps have no
standard way for a model to say "run me with this build". So every launch comes
with its own install instructions.

Checked on 2026-09-28:

- The llama.cpp router starts every model's child process with its own
  executable (`bin_path` in `tools/server/server-models.cpp`). No preset key
  chooses another program.
- The nearest precedent is `trust_remote_code` in Transformers: a model repo
  brings its own code until support is upstream, with an explicit opt-in.

First case: Prism ML's Bonsai 2 27B (`prism-ml/Ternary-Bonsai-2-27B-gguf`). Its
PQ2_0 and PTQ1_0 weights load only with Prism's fork
(`PrismML-Eng/llama.cpp`). Upstream support is in progress.

## Goal

Ship llamaenv next to the regular Llama app so that a model that needs a
custom llama.cpp build runs with it automatically, and nothing else changes.

The same mechanism also switches between official llama.cpp versions: per
model, or as the default runtime. Custom builds are the first use case.

## Principle: peripheral, minimal disruption

This principle comes before every other requirement. When a design choice is
unclear, choose the option that keeps users closer to the standard llama.cpp
path.

1. **The standard path stays the standard path.** Users install and use the
   official llama.cpp build and the Llama app as documented. llamaenv adds
   one thing: the right build for the few models that need a custom one.
2. **Step in only when needed.** For a model without a runtime mapping,
   llamaenv only passes traffic through. It adds no behavior, no settings,
   and no user interface of its own.
3. **Fail toward the standard path.** If llamaenv cannot start its
   switcher, it runs the official `llama serve` directly, so the Llama app and
   all officially supported models still work. Only mapped models are then
   unavailable, with a clear error.
4. **No new habits.** Users keep the official commands, the Llama app, and the
   llama.cpp web page. llamaenv's own commands are for setup and diagnosis,
   not for daily use.
5. **Easy to remove.** Uninstalling or deleting llamaenv returns the machine
   to a plain standard setup with no leftovers to clean up.
6. **Temporary by intent.** A runtime mapping exists only until the model's
   support is upstream. Then the mapping is removed and the model runs on the
   official build.
7. **The default is the standard build.** Unless the user chooses another
   default, unmapped models run on the official `llama` that the standard
   installer manages. Pinning a version is always an explicit choice.

## Functional requirements

1. **Runtimes.** A runtime is either an official llama.cpp version (for
   example `b11200` from llama.app's bucket) or a custom build (for example
   Prism's fork). The built-in runtime `official` is the `llama` that the
   standard installer manages.
2. **Per-model runtime.** A model that is mapped to a runtime runs with that
   runtime. Every other model runs with the default runtime.
3. **Default runtime.** The default is `official`. The user can set another
   default, for example a pinned official version, and set it back.
4. **Switch on the fly.** Selecting a model in the Llama app, the llama.cpp web
   page, or any OpenAI-compatible client selects its runtime. No restart and no
   manual step.
5. **One address.** Clients see one server with one model list that contains
   the models of every runtime.
6. **Future-compatible config.** The mapping uses a llama.cpp preset key,
   `runtime`, so the same config works if llama.cpp adds the feature later.
7. **Mapping sources,** highest first:
   1. the user's local override;
   2. the model repo's own `preset.ini` on the Hub with a `runtime` key, set
      by the model developer;
   3. a shared list on the Hub for repos whose owner has not added one.
8. **Models are matched by Hub repo and quant,** for example
   `prism-ml/Ternary-Bonsai-2-27B-gguf:PQ2_0`, never by name patterns. This is
   the model ID that the Llama app uses.
9. **Runtimes come from pinned sources:** a local path, or a bucket in
   llama.app's layout (`<version>/<arch>/<os>/<backend>/...`). A downloaded
   runtime is checked against its SHA-256 hash.
10. **Explicit trust.** A runtime named by a model repo or the shared list runs
   only after the user allows it once. A runtime that the user adds is
   trusted.
11. **Visible decisions.** `llamaenv list` shows every known model, its
   runtime, and the layer that decided it.
12. **Commands:** `install`, `uninstall`, `status`, `list`, `versions`,
    `use`, `runtime add`, `runtime remove`, `map`, `unmap`, `trust`.

## Requirements for the Llama app and official llama.cpp

1. **No disruption.** Models without a runtime mapping behave exactly as with
   the official `llama serve`, including streaming responses, downloads,
   loading, unloading, idle unload, and the web page.
2. **No file changes outside llamaenv's own folder.** llamaenv never
   edits the Llama app's settings, preset, or PID file, and never edits or
   replaces the official `llama` files.
3. **Shadow, do not replace.** llamaenv puts its own `llama` in its own
   folder first on the user PATH. Official updates keep working, because they
   write only their own files.
4. **Transparent lifecycle.** When the Llama app stops the server process, all
   runtimes that llamaenv started stop too.
5. **Real version.** `llama --version` reports the official build's version.
6. **Official build always present.** `llamaenv install` makes sure that
   the official `llama` is installed, through llama.app's own install script.

## Removal safety

1. After `llamaenv uninstall`, the Llama app starts the official server and
   all officially supported models work.
2. The same holds after the llamaenv folder is deleted by hand.
3. Model files in the shared Hugging Face cache stay. A model that needs a
   custom runtime then fails to load with the official build; every other model
   works. Uninstall offers to delete such model files.

Basis: the Windows Llama app looks up `llama.exe` on PATH at every start,
does not store the path, and skips a PATH entry whose file does not exist
(`LlamaApp.LlamaCpp/LlamaManager.cs`).

## Platforms

Windows, Linux, and macOS are supported. Windows comes first, because it is the
first use case.

1. **Windows** x64 and arm64, with the Llama app and with `llama serve`.
2. **Linux** x64 and arm64, with `llama serve` from llama.app.
3. **macOS** arm64, with the Llama app and with `llama serve`, for every
   install method of llama.app: `install.sh`, the app's own download,
   Homebrew, and a build in `/usr/local/bin`. The app does not use PATH, so
   llamaenv uses the exception in
   [the design principles](DESIGN_PRINCIPLES.md#macos-the-llama-app-does-not-use-path).

## Non-goals

- Changes to llama.cpp or to the Llama apps.
- Running two runtimes for one model at the same time.
- Building runtimes. llamaenv uses builds that a model developer publishes.

## Success criteria

On a Windows machine with the Llama app, an NVIDIA GPU, and llamaenv:

1. Bonsai 2 27B and an officially supported model both appear in the app. Both
   answer, and each runs on its own build.
2. Switching between them in the app needs no manual step.
3. After uninstalling llamaenv, the officially supported model still works
   in the app.
