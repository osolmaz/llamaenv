# Sets up Bonsai 2 27B for the Llama app on Windows with an NVIDIA GPU.
# Needs llamaenv.exe on PATH, or pass its path: .\install-bonsai.ps1 -Llamaenv C:\path\llamaenv.exe
param([string]$Llamaenv = "llamaenv")
$ErrorActionPreference = "Stop"
$here = Split-Path -Parent $MyInvocation.MyCommand.Path
$release = "https://github.com/PrismML-Eng/llama.cpp/releases/download/prism-b10743-adfffbe"

function Run { & $Llamaenv @args; if ($LASTEXITCODE -ne 0) { throw "llamaenv $args failed" } }

# 1. Prism's CUDA build and its CUDA runtime, pinned in runtimes.lock.
Run runtime add prism "$release/llama-prism-b10743-adfffbe-bin-win-cuda-13.3-x64.zip" "$release/cudart-llama-bin-win-cuda-13.3-x64.zip"

# 2. Bonsai runs on it. Every other model stays on the official llama.
Run map prism-ml/Ternary-Bonsai-2-27B-gguf prism

# 3. Bonsai's llama.cpp settings, a plain llama.cpp preset.
Run preset prism (Join-Path $here "prism.ini")

# 4. The shim first on PATH (and the official llama when missing), then a
#    restart of the Llama app, so it picks up the shim.
Run install

# Then download Bonsai in the Llama app as usual, for example PQ2_0.
