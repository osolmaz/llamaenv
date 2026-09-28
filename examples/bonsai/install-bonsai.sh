#!/bin/sh
# Sets up Bonsai 2 27B for "llama serve" on Linux x64 with an NVIDIA GPU.
# Needs llamaenv on PATH, or set LLAMAENV=/path/to/llamaenv.
set -eu
llamaenv=${LLAMAENV:-llamaenv}
here=$(cd "$(dirname "$0")" && pwd)
release=https://github.com/PrismML-Eng/llama.cpp/releases/download/prism-b10743-adfffbe

# 1. Prism's CUDA build, pinned in runtimes.lock. Prism publishes no Linux arm64
#    CUDA build; there, build it and use "runtime add prism <folder>".
"$llamaenv" runtime add prism "$release/llama-prism-b10743-adfffbe-bin-linux-cuda-13.3-x64.tar.gz"

# 2. Bonsai runs on it. Every other model stays on the official llama.
"$llamaenv" map prism-ml/Ternary-Bonsai-2-27B-gguf prism

# 3. Bonsai's llama.cpp settings, a plain llama.cpp preset.
"$llamaenv" preset add prism "$here/bonsai-2-27b.ini"

# 4. The shim first on PATH, and the official llama when missing.
"$llamaenv" install

# Then open a new shell and run "llama serve" as usual.
