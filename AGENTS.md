# AGENTS.md - llamaenv

- Status: planning. Do not implement until the plan in `docs/` is approved.
- llamaenv is work in progress and temporary. It may be deprecated or
  absorbed into llama.cpp. Do not build features that would make it harder to
  remove.
- llamaenv is peripheral. Keep users on the standard llama.cpp path and
  step in only for models with a runtime mapping. Pass everything else through
  unchanged, and fall back to the official `llama serve` when llamaenv
  fails. When a choice is unclear, pick the option with less disruption.
- Platforms: Windows and Linux. Windows comes first.
- Scope: llama.cpp runtimes, meaning official versions and custom builds, per
  model or as the default. The default stays `official`, the standard build,
  unless the user chooses otherwise.
- Follow `docs/2026-09-28-requirements.md` and
  `docs/2026-09-28-implementation-plan.md`. Update them when a decision changes.
- Never hardcode model names or families in code. Models map to runtimes only
  through config: local overrides, a model repo's `preset.ini`, or the shared
  list.
- Never edit or replace files that belong to the Llama app or to the official
  `llama` install. llamaenv writes only inside its own folder.
- Removal safety is a hard requirement: after uninstalling or deleting
  llamaenv, the Llama app and every officially supported model must keep
  working. Keep the end-to-end removal tests.
