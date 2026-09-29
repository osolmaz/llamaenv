---
date: 2026-09-29
author: Onur Solmaz <2453968+osolmaz@users.noreply.github.com>
title: llamaenv doctor and model commands on the model's runtime
tags: [llamaenv, plan, doctor, fit-params, macos, llama-app]
---

# llamaenv doctor and model commands on the model's runtime

Status: planned, not started.

This plan covers two changes that came out of the macOS Llama app work on
2026-09-29 (pull request #6). Both keep to
[the design principles](DESIGN_PRINCIPLES.md).

## 1. Commands for a mapped model run on its runtime

### Problem

The macOS Llama app measures each model's memory before it offers context
sizes. It runs `llama fit-params` twice
(`ggml-org/Llama-macOS` 0.42.0, `Llama/Server/MemProfile.swift`, line 117):

```sh
llama fit-params -m <model file> -c 4096 -fitp on
llama fit-params -m <model file> -c 131072 -fitp on
```

The shim sends every command except `serve` to the official `llama`. The
official build cannot load Bonsai 2 27B, so the probe fails and the app falls
back to its own estimate. On 2026-09-29 the app gave Bonsai a context of 4096
in its `models.ini`. That the failed probe causes exactly this value is likely,
but not verified.

Measured on 2026-09-29 on an M5 Mac with the Bonsai PQ2_0 file:

| Build | Result of `fit-params -m <Bonsai> -c 4096 -fitp on` |
| --- | --- |
| official, Homebrew 10470 | crash: `failed to load model` |
| Prism `prism-b10743-adfffbe` | `MTL0 6539 405 509` and `Host 322 0 48`, in 0.24 s |

The app's context tiers are 4K, 8K, 16K, 32K, 64K, 128K, and 256K
(`Llama/Models/ContextTier.swift`). There is no 90K tier, so this change fixes
the app's estimate and its offered tiers, not a context of exactly 98304. For
that, the user sets `ctx-size` in the app's `models.ini`, which the app's own
comments allow.

### Rule

Every `llama` command except `serve` that names a mapped model runs on that
model's runtime, with the same arguments. Every other command, and every
command for an unmapped model, runs on the official `llama` unchanged
(principle 3). This covers `fit-params` for the app, and `llama run` or
`llama bench` in a terminal. There is no special case for `fit-params`.

### How the shim finds the model

- `-hf <repo[:quant]>` or `--hf-repo <repo[:quant]>`: the model ID as it is.
- `-m <path>` or `--model <path>`: when the path is in the Hugging Face cache,
  `.../hub/models--<org>--<name>/snapshots/<revision>/<file>`, the repository
  is `<org>/<name>`. The mapping matches the repository, as
  `Config.RuntimeFor` already does for `<repo>:<quant>`.
- Anything else, such as a path outside the cache: the model is unmapped, and
  the command runs on the official `llama`.

The code names no model (principle 6); the choice comes from `runtimes.ini`.

### The runtime's program

Use `Runtime.Launch()`, as the switcher does. A runtime that has only
`llama-server`, with no unified `llama`, cannot run other commands: then the
command fails with a clear error that names the runtime.

### Failures

When the mapped runtime is missing or broken, the command fails with a clear
error that names the runtime and the model (principle 4: a broken mapping
affects only the mapped models, with a clear error). It does not fall back to
the official `llama`, which cannot load such a model anyway.

### Weak spot

Reading the repository from a cache folder name depends on Hugging Face's
cache layout. The layout is documented and stable, but llamaenv does not own
it. A path that does not match falls toward the standard path.

### Tests

- `fit-params -m <cache path of a mapped repo>` runs the runtime's program with
  the same arguments; the fake `llama` in `TestMain` records them.
- The same for `-hf` and `--hf-repo`, with and without `:quant`.
- An unmapped model, a path outside the cache, and a command without a model
  run the official `llama` unchanged.
- A missing runtime and a runtime without `llama` give a clear error and a
  non-zero exit.
- Live on macOS: after `llamaenv install`, remove the app's cached profile for
  Bonsai, restart the app, and check that its log shows the profile from
  Prism's build.

## 2. `llamaenv doctor`

### Rule

Principle 7 now says how diagnosis works. `llamaenv doctor` reads and reports
on any file, and names the exact change for a file that llamaenv does not own.
`llamaenv doctor --fix` fixes only llamaenv's own files. It never edits the
files of llama.cpp, the official `llama`, or the Llama app.

It sits next to `llamaenv status` and `llamaenv logs` from
[the diagnostics design](2026-09-29-diagnostics.md).

### Checks

Each check prints one plain line: what is wrong, why it matters, and the fix.

| Check | Owner | Fix |
| --- | --- | --- |
| A runtime preset section names no model: no `model`, `hf-repo`, or `hf` key. It becomes a model that cannot load. | llamaenv | `--fix` cannot guess the file; report the section and the key to add. |
| The Llama app's name for a mapped model's file differs from the preset's section name, so the list shows two entries. Found on macOS, where the app names Bonsai PQ2_0 `:unknown`. | the app | Report both names and say which one gets the preset. |
| On macOS, the app runs a `llama` other than the shim, for example after an app update, `install.sh`, or `brew upgrade`. | llamaenv | `--fix` runs the same take as `llamaenv install`. |
| `official/slot.json` exists, but the kept `llama` is missing or does not run. | llamaenv | Report; `--fix` removes the stale record only when the slot holds a working `llama`. |
| Homebrew's formula is unlinked, and no llamaenv record says that llamaenv unlinked it. | Homebrew | Report `brew link <formula>`. |
| A mapped runtime is missing, not trusted, or fails `llama version`. | llamaenv | Report `llamaenv runtime add` or `llamaenv trust`. |
| The switcher's state names a PID that no longer runs. | llamaenv | `--fix` removes the stale state folder. |

### Output and exit code

Every line is marked `ok`, `warn`, or `fail`. Exit 0 when nothing fails, and
1 otherwise, so scripts can use it. `--fix` prints each change that it made.

### Tests

One test per check, with a temporary folder like the macOS slot tests. Each
test also checks that `doctor` without `--fix` changes no file.

## Related, outside llamaenv

The Mac app names the Bonsai file `:unknown`, because its quant list
(`Llama/HF/GGUFQuant.swift`) is a copy of `GGMLFileQuantizationType` in
`huggingface/huggingface.js` (`packages/tasks/src/gguf.ts`), which has no
`PQ2_0` yet. When huggingface.js and the app add it, the app and the preset use
the same name again, and the double entry goes away.
