---
date: 2026-09-28
author: Onur Solmaz <2453968+osolmaz@users.noreply.github.com>
title: llamaenv design principles
tags: [llamaenv, llama-cpp, architecture, design]
---

# llamaenv design principles

llamaenv adds one thing next to the standard llama.cpp setup: it runs a model
with the llama.cpp build that the model needs. It MUST NOT replace, copy, or
reinterpret behavior that llama.cpp, llama.app, or the Llama app already
provide.

llamaenv is a stopgap. It may be deprecated, or its idea absorbed into
llama.cpp. Every design choice must keep it easy to remove.

## Requirement terms

`MUST` and `MUST NOT` mark requirements. `SHOULD` and `SHOULD NOT` mark the
expected design unless the pull request gives a concrete reason for an
exception. `MAY` marks an allowed choice.

## The first design question

Before adding a setting, file, field, endpoint, default, or behavior, a
contributor MUST ask:

> Does llama.cpp, llama.app, or the Llama app already represent or do this?

The contributor MUST check the source of the pinned upstream version:
`ggml-org/llama.cpp` (router, presets, server options), `ggml-org/llama-install.sh`
(install layout), and `ggml-org/Llama-Windows` (how the app starts and talks to
the server). The pull request MUST name the files it checked.

If upstream already provides the behavior, llamaenv MUST use it as it is.
llamaenv MUST NOT add an alias, a mirrored setting, a second default, or a
wrapper that renames the same concept.

## Who owns what

llama.cpp owns:

- serving models, the router, and the child process per model
- model download, the Hugging Face cache, and the model list
- every server option, such as `ctx-size`, `parallel`, and `n-gpu-layers`, and
  their defaults
- presets: the INI format, `--models-preset`, and `preset.ini` on the Hub
- the web page and every API endpoint

llama.app owns the install of the official `llama` and its location.

The Llama app owns its user interface, its server lifecycle (start, stop,
restart, adopt), its settings, and its choices such as a model's context size.

llamaenv owns only:

- the runtime sources, their pinned hashes, and the unpacked runtimes
- the mapping from a model to a runtime
- the shim, and the switcher that routes requests and merges lists
- its own install, uninstall, and status

## Principles

### 1. External and peripheral

llamaenv MUST stay outside llama.cpp and the Llama app. It MUST NOT patch,
fork, or configure them beyond what their public interfaces allow. Users MUST
keep the standard path: the official installer, the official `llama`, the
Llama app, and the llama.cpp web page.

### 2. Shadow, never replace

llamaenv MUST reach the Llama app only by putting its own `llama` first on the
user PATH. It MUST NOT edit, move, or replace the official `llama`, the Llama
app's files, or its settings.

The only exception is install and uninstall: they MAY stop and restart the
Llama app and the server that it started, so that the app looks up `llama`
again. They MAY read the app's PID file for that. They MUST NOT write it. When llamaenv
runs outside the desktop session, for example over SSH, they MAY start the app
through a one-time scheduled task with an interactive logon, which runs it on
the user's desktop. They MUST delete the task once it has run.

### 3. Pass through, unchanged

For a model without a runtime mapping, llamaenv MUST behave exactly like the
official `llama serve`: the same arguments, environment, defaults, addresses,
API answers, and web page. The web page and its files MUST always come from the
default router. Routing by model applies to API requests only.

When llamaenv adds nothing, it MUST add no process at all: without mappings,
the shim runs the official `llama` directly.

### 4. Fail toward the standard path

When llamaenv cannot do its part, it MUST fall back to the official
`llama serve` with the original arguments. A broken mapping or a missing
runtime MUST affect only the mapped models, with a clear error. Officially
supported models MUST keep working.

### 5. Keep the formats apart

See [how llama.cpp is configured](2026-09-28-llama-cpp-config.md) for its
config layers and presets.

llamaenv's own files MUST hold only llamaenv concepts: runtime sources, pins,
the model-to-runtime mapping, and the names of a runtime's presets.

llama.cpp settings MUST live in a standard llama.cpp preset, with llama.cpp's
format, key names, and meaning. llamaenv MUST pass such a preset to llama.cpp
unchanged, and MUST NOT add its own keys to it. A preset that llamaenv uses
MUST also work with `llama serve --models-preset` directly.

A runtime's presets MUST go only to that runtime's router. Each model's
settings SHOULD be their own preset file, so that setting up one model never
changes another. The default router
MUST get the client's arguments and preset unchanged.

The router takes one `--models-preset`. When the client passes its own preset,
or the runtime has several presets, llamaenv MAY write one combined file for
the runtime's router: the runtime presets in the order they were added, and
the client's preset over them, key by key, so the client's values win. It
MUST only copy text: no checks, no renamed keys, no added keys. It
MUST write the file in its own state folder, and write it again before it
passes on a reload.

Where a llamaenv format looks like a llama.cpp format, it MUST NOT pretend to
be one. Its file name and documentation MUST say that it is llamaenv's.

### 6. The model, not the code, decides

Code MUST NOT name models or model families. A model maps to a runtime only
through configuration: a local mapping or a shared list. (A `preset.ini` in a
model repository cannot carry it: llama.cpp then treats the repository as a
preset repository and downloads no model files.) Settings for a model, such as its context size, MUST come from a
preset, not from code.

### 7. No new user interface

llamaenv MUST NOT add a user interface, a new page, or new behavior in the web
page or the Llama app. Its commands are for setup and diagnosis.

A diagnosis command, such as `llamaenv doctor`, MAY read and report on any
file, including the files of llama.cpp, the official `llama`, and the Llama
app. For a file that llamaenv does not own, it MUST name the file and the
exact change, and leave the change to the user. It MAY fix llamaenv's own
files, and only when the user asks for it explicitly, such as with `--fix`. It
MUST NOT edit another program's files, even when asked: the change would stay
after uninstall (principle 8), and the program that owns the file may write it
again.

### 8. Removable without leftovers

After `llamaenv uninstall`, or after its folder is deleted by hand, the Llama
app and every officially supported model MUST work as if llamaenv had never
been installed. llamaenv MUST NOT store anything outside its own folders,
except its one PATH entry.

### 9. Pinned and trusted

A downloaded runtime MUST be checked against a pinned SHA-256. A runtime that
comes from a model repository or a shared list MUST run only after the user
allowed it once.

### 10. One instance, one state

Several servers can run at the same time, for example the Llama app's server
and a test server. Each llamaenv switcher MUST keep its own state, and MUST NOT
overwrite another instance's state.

## Platform exceptions

### macOS: the Llama app does not use PATH

Checked: `ggml-org/Llama-macOS` 0.42.0 (`Llama/Engine/LlamaBinaries.swift`,
`Llama/Engine/LlamaInstaller.swift`, `Llama/Engine/LlamaInstallManager.swift`),
`ggml-org/llama-install.sh` (`install.sh`), and Homebrew's `keg.rb` and
`formula_installer.rb`.

The macOS Llama app runs the first of these files that exists:
`~/.llama-app/llama`, `/opt/homebrew/bin/llama`, and `/usr/local/bin/llama`.
It never reads PATH, so principle 2 cannot reach it. The first file is where
both llama.app's `install.sh` and the app itself put the official `llama`, and
the app keeps it at the version that it pins.

On macOS, llamaenv MAY break principles 2 and 8 in these ways, and only in
these ways:

- It MAY take the file that the app runs now: move that program into its own
  `official/` folder, or keep a symlink to where a symlink pointed, and put a
  symlink to its shim in its place. The kept program is the official `llama`.
- When that file is Homebrew's, it MAY run `brew unlink <formula>` and put the
  shim symlink at the next free place in the app's list. The official `llama`
  is then the formula's `opt` path, which stays in place while unlinked and
  across upgrades. Homebrew owns the symlinks in its bin folder, so llamaenv
  MUST NOT write them.
- It MAY use sudo for these changes in a folder that root owns, such as
  `/usr/local/bin`.

It MUST record what it changed in its own folder, and uninstall MUST undo
exactly that: put the kept program or symlink back, and link the formula again
only when llamaenv unlinked it and nothing linked it since.

When the app runs another file, for example after the app updates its own
`llama`, after `install.sh` runs again, or after `brew upgrade` links the
formula again, llamaenv is off and the app runs the standard path. Uninstall
then leaves that file as it is. `llamaenv status` MUST report this and name the
command that turns llamaenv on again. llamaenv MUST NOT watch for it or undo it
by itself.

When the llamaenv folder is deleted by hand, the shim symlink points nowhere,
so the app skips it and installs its own official `llama`. The symlink is a
leftover. With Homebrew, the formula also stays unlinked, because the record
that uninstall uses to link it again was in that folder; `brew link <formula>`
links it again. Keeping the record anywhere else would break principle 8 for
every install.

Remove this exception when the macOS app looks up `llama` on PATH.

## Known violations

None on 2026-09-29.

Fixed on 2026-09-29:

- `llamaenv install` restarted the Llama app with `Start-Process`. From a
  session without the desktop, such as a remote shell, the app started in that
  session instead of on the user's desktop (principle 2).

Fixed on 2026-09-28:

- The web page was sent to a mapped runtime when its URL named a model
  (principle 3), and a download unloaded the other runtime's models
  (principle 3).
- llama.cpp settings sat in llamaenv's `models.ini`, and that file looked like
  a llama.cpp preset but matched models by other rules (principle 5).
  `models.ini` is gone: `runtimes.ini` has one `[runtime <name>]` section per
  runtime with its `models` list, and settings live in a plain preset that the
  `presets` key lists, one file per model.
- All switchers wrote one `state/switcher.json` (principle 10). Each switcher
  now has its own `state/<port>/` folder, removed when it stops.
