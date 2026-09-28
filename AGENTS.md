# AGENTS.md - llamaswitch

- Status: planning. Do not implement until the plan in `docs/` is approved.
- Follow `docs/2026-09-28-requirements.md` and
  `docs/2026-09-28-implementation-plan.md`. Update them when a decision changes.
- Never hardcode model names or families in code. Models map to runtimes only
  through config: local overrides, a model repo's `preset.ini`, or the shared
  list.
- Never edit or replace files that belong to the Llama app or to the official
  `llama` install. llamaswitch writes only inside its own folder.
- Removal safety is a hard requirement: after uninstalling or deleting
  llamaswitch, the Llama app and every officially supported model must keep
  working. Keep the end-to-end removal tests.
