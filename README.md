# llamaswitch

llamaswitch runs each model with the llama.cpp build that it needs, next to the
regular [Llama app](https://github.com/ggml-org/Llama-Windows) and
[llama.app](https://llama.app) install.

Some models need a custom llama.cpp build until their support is upstream. An
example is Prism ML's Bonsai 2 27B, whose ternary weights load only with Prism's
llama.cpp fork. llamaswitch sits in front of `llama serve`, sends requests for
such a model to its custom build, and sends everything else to the official
build. The Llama app and models that llama.cpp already supports keep working as
before. Removing llamaswitch leaves a normal, working Llama setup.

llamaswitch is also a working example of a feature that llama.cpp could have
later: a per-model `runtime` key in its presets.

Status: planning. Nothing is implemented yet.

- [Requirements](docs/2026-09-28-requirements.md)
- [Implementation plan](docs/2026-09-28-implementation-plan.md)
