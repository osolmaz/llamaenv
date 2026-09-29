---
date: 2026-09-29
author: Onur Solmaz <2453968+osolmaz@users.noreply.github.com>
title: Changes that belong upstream
tags: [llamaenv, upstream, llama-cpp, llama-app, macos, huggingface-js]
---

# Changes that belong upstream

llamaenv works around these gaps from the outside. Each one belongs in another
project. When a fix lands there, remove the matching workaround from llamaenv.
Nothing here has been reported yet.

## 1. llama.cpp: a per-model runtime

**Project:** `ggml-org/llama.cpp`.

**Gap:** a preset cannot say which build runs a model. The router starts every
child with its own executable (`bin_path` in `tools/server/server-models.cpp`),
so a model that needs a fork, such as Bonsai 2 27B, needs a second router.

**Change:** a per-model preset key that names the runtime, and a way for
`llama` to fetch and trust a pinned build.

**Then:** llamaenv is no longer needed, as
[the implementation plan](2026-09-28-implementation-plan.md#11-future-path)
says.

## 2. llama.cpp: PQ2_0 and PTQ1_0 weights

**Project:** `ggml-org/llama.cpp`, with `PrismML-Eng/llama.cpp`.

**Gap:** Bonsai 2 27B's PQ2_0 and PTQ1_0 weights load only with Prism's fork.
On 2026-09-29, the official build (Homebrew 10470) stopped with
`failed to load model` on the PQ2_0 file.

**Then:** remove the `prism` runtime mapping for Bonsai. The model runs on the
official build.

## 3. Llama app for macOS: find `llama` on PATH

**Project:** `ggml-org/Llama-macOS` (checked: 0.42.0).

**Gap:** the app never reads PATH. `LlamaBinaries.resolve()`
(`Llama/Engine/LlamaBinaries.swift`) runs the first program of a fixed list:
`~/.llama-app/llama`, `/opt/homebrew/bin/llama`, and `/usr/local/bin/llama`.
The Windows app looks up `llama.exe` on PATH at every start
(`LlamaApp.LlamaCpp/LlamaManager.cs`).

**Workaround in llamaenv:** the macOS exception in
[the design principles](DESIGN_PRINCIPLES.md#macos-the-llama-app-does-not-use-path).
`llamaenv install` takes the place of the file that the app runs, keeps that
official `llama` in llamaenv's folder, and for Homebrew unlinks the formula and
puts the shim at `/usr/local/bin/llama`. This breaks design principles 2 and 8.
An update of the app's `llama`, `install.sh`, or `brew upgrade` turns
llamaenv off again.

**Change:** `resolve()` looks up `llama` on the user's login-shell PATH first,
as the Windows app does. A GUI app gets only launchd's PATH, so it must ask
the login shell. When nothing is found there, it keeps the current list.

**Then:** remove the macOS exception, `internal/install/appslot.go`, and the
Homebrew handling. On macOS, llamaenv then only adds its PATH entry, as on the
other systems. It may also need to write its PATH block to `~/.zprofile`,
because a login zsh does not read `.profile`.

## 4. huggingface.js and the Llama app for macOS: the PQ2_0 quant name

**Projects:** `huggingface/huggingface.js`, then `ggml-org/Llama-macOS`.

**Gap:** `GGMLFileQuantizationType` and `parseGGUFQuantLabel` in
`packages/tasks/src/gguf.ts` have `TQ1_0` and `TQ2_0`, but no `PQ2_0` (checked
on 2026-09-29). The macOS app copies that list on purpose
(`Llama/HF/GGUFQuant.swift`), so that a quant link from the Hub matches the
same file. For a file name that has no known quant, the app uses `unknown`.
So it names Bonsai's file `prism-ml/Ternary-Bonsai-2-27B-gguf:unknown`, while
the Windows app and llamaenv's preset name it `:PQ2_0`.

**Effect:** the macOS app lists Bonsai twice, `:PQ2_0` from the preset and
`:unknown` from the app. Only `:PQ2_0` gets the preset's settings.

**Change:** add `PQ2_0`, and `PTQ1_0` if Prism uses it in file names, to
huggingface.js, then to the macOS app's copy.

**Then:** remove the note on `:unknown` from `examples/bonsai/bonsai-2-27b.ini`.

## 5. Llama app for macOS and llama.cpp: the case of the quant tag

**Projects:** `ggml-org/Llama-macOS` and `ggml-org/llama.cpp`.

**Gap:** the app writes `[prism-ml/Ternary-Bonsai-2-27B-gguf:unknown]` in its
`models.ini`, and opens the web page with
`?model=prism-ml/Ternary-Bonsai-2-27B-gguf:unknown&load=true`. The router
lists the same model as `...:UNKNOWN`. The web page matches names exactly, so
it shows "Model Not Available". This happens without llamaenv too: on
2026-09-29 the app's plain Homebrew server also listed `:UNKNOWN`. Where the
tag becomes upper case is not verified.

**Change:** the app and llama.cpp use one spelling for a model ID, or the web
page matches the quant tag without regard to case. Fixing item 4 hides this
for Bonsai, but not for other files without a known quant.

## 6. Llama app for macOS: the context for a model that the official build cannot load

**Project:** `ggml-org/Llama-macOS`.

**Gap:** the app always writes a `ctx-size` for each model. It sizes models
with `llama fit-params` (`Llama/Server/MemProfile.swift`), which fails on a
model that the official build cannot load, and then it uses its own estimate.
On 2026-09-29 it gave Bonsai 4096. Its context tiers
(`Llama/Models/ContextTier.swift`) have no value between 64K and 128K.

**llamaenv's part:** running `fit-params` for a mapped model on its runtime
is planned in
[the doctor plan](2026-09-29-doctor-and-fit-params-plan.md). The app side is
only a note: a failed probe could say so in the app, instead of a small
context without a reason.

## 7. Llama app for macOS: no restart after a server crash

**Project:** `ggml-org/Llama-macOS`.

**Gap:** after its server was killed with SIGKILL on 2026-09-29, the app did
not start a new one until it was restarted. This is the same with the official
`llama`, and it does not affect llamaenv. It is only a note.
