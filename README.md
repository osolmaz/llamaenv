# llamaenv

llamaenv is a runtime manager for llama.cpp that works next to the standard
[Llama app](https://github.com/ggml-org/Llama-Windows) and
[llama.app](https://llama.app) install. It runs each model with the llama.cpp
build that the model needs, and every other model with the official build.

Some models need a custom llama.cpp build until their support is upstream. An
example is Prism ML's Bonsai 2 27B, whose ternary weights load only with
Prism's llama.cpp fork. With llamaenv, you download and select Bonsai in the
Llama app or the llama.cpp web page as usual, and it runs on Prism's build.
Officially supported models keep running on the official build. llamaenv
switches between them when you select a model.

llamaenv can also pin or switch llama.cpp versions: keep one model on an older
official version, or try a newer version as the default.

> **Work in progress.** llamaenv is a stopgap until llama.cpp can do this
> itself. When it can, remove llamaenv.

llamaenv stays out of the way. The official installer, the official `llama`,
and the Llama app stay as they are. Models without a mapping run exactly as
without llamaenv. If llamaenv fails or is removed, everything runs the
standard way.

Platforms: Windows and Linux.

## Install

Build it with Go 1.26:

```sh
go build -o llamaenv .
```

`llamaenv install` puts a `llama` shim first on your user PATH, and installs
the official `llama` from llama.app when it is missing. On Windows it also
restarts the Llama app, so the app picks up the shim.

## Set up a model

Add the build that the model needs, map the model to it, and optionally add a
llama.cpp preset with settings for the model:

```sh
llamaenv runtime add prism <folder with llama-server | archive URLs...>
llamaenv map prism-ml/Ternary-Bonsai-2-27B-gguf prism
llamaenv preset add prism bonsai-2-27b.ini
llamaenv install
```

A preset is a plain llama.cpp preset, the same file that
`llama serve --models-preset` reads:

```ini
[prism-ml/Ternary-Bonsai-2-27B-gguf:PQ2_0]
ctx-size = 98304
parallel = 1
```

Only that build's router gets it. Each model gets its own preset file, so
setting up another model on the same build keeps this one. A context size that
you set in the Llama app still wins over the preset.

[`examples/bonsai`](examples/bonsai) has complete setup scripts for Windows and
Linux, and a preset for Bonsai 2 27B.

After setup, download the model in the Llama app, or run `llama serve` as
usual.

## Everyday commands

```sh
llamaenv list        # which model uses which runtime and preset
llamaenv status      # the running switchers and their routers
llamaenv versions    # official llama and runtime versions
llamaenv use <name>  # a different default runtime; "official" goes back
llamaenv unmap <model>
llamaenv uninstall   # back to the plain standard setup
```

Downloaded model files stay in the Hugging Face cache when you uninstall.
