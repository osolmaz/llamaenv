# llamaenv

llamaenv manages llama.cpp runtimes, next to the regular
[Llama app](https://github.com/ggml-org/Llama-Windows) and
[llama.app](https://llama.app) install. A runtime is an official llama.cpp
version or a custom build, such as a model developer's fork. llamaenv runs each
model with the runtime it needs, and it can switch the default runtime, like
`pyenv` does for Python versions.

Some models need a custom llama.cpp build until their support is upstream. An
example is Prism ML's Bonsai 2 27B, whose ternary weights load only with Prism's
llama.cpp fork. llamaenv sits in front of `llama serve`, sends requests for
such a model to its custom build, and sends everything else to the official
build. The Llama app and models that llama.cpp already supports keep working as
before. Removing llamaenv leaves a normal, working Llama setup.

llamaenv can also pin or switch llama.cpp versions: for example, keep one model
on an older official version that works better for it, or try a newer version
as the default. Without such a choice, llamaenv follows the official build that
the standard installer manages.

llamaenv is also a working example of a feature that llama.cpp could have
later: a per-model `runtime` key in its presets.

> **Work in progress.** llamaenv is a stopgap. It may be deprecated, or its
> idea absorbed into llama.cpp itself, for example as a per-model `runtime`
> preset key. When that happens, llamaenv should be removed.

Status: first implementation. Tested end to end on Linux (the official llama
b11200 next to Prism's build, switching between an official model and Bonsai 2
27B). Windows is the first target and comes next.

llamaenv is peripheral by design. It stays out of the way of the standard
llama.cpp path: the official installer, the official `llama` build, and the
Llama app stay as they are, and llamaenv only steps in for models that need
a custom build. If llamaenv fails or is removed, everything runs the
standard way.

Platforms: Windows and Linux. Windows comes first.

## Use

Build it with Go 1.26: `go build -o llamaenv .`. Then:

```sh
llamaenv install                 # the official llama if missing, then the shim first on PATH
llamaenv runtime add prism <folder with llama-server | archive URLs...>
llamaenv map prism-ml/Ternary-Bonsai-2-27B-gguf prism
```

Restart the Llama app, or run `llama serve` as usual. Bonsai now runs on
Prism's build, and every other model on the official build. Select either one
in the Llama app or the llama.cpp web page; llamaenv switches under the hood.

```sh
llamaenv list        # which model uses which runtime
llamaenv status      # the running routers
llamaenv versions    # official llama and runtime versions
llamaenv uninstall   # back to the plain standard setup
```

Without any mapping, `llama serve` runs the official llama unchanged.

## Docs

- [Requirements](docs/2026-09-28-requirements.md)
- [Implementation plan](docs/2026-09-28-implementation-plan.md)
- [Spike findings](docs/2026-09-28-spike-findings.md)
