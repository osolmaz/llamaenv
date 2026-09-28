# AGENTS.md - llamaenv

- You MUST read [the design principles](docs/DESIGN_PRINCIPLES.md) before you change behavior, config, or files. They say what llama.cpp and the Llama app own, and what llamaenv may do.
- Read [how llama.cpp is configured](docs/2026-09-28-llama-cpp-config.md) before you touch presets or settings.
- Status: alpha. There are no users to keep compatible. Change config, files, commands, and state in place, and delete what they replace. Do not add migrations, fallback readers, aliases, deprecation paths, or any other backward compatibility.
- Follow the plan in `docs/`, and update it when a decision changes.
- Before finishing a change, run these, as CI does:
  ```sh
  gofmt -l .                      # must print nothing
  go vet ./... && GOOS=windows go vet ./...
  go test ./...
  golangci-lint run ./... && GOOS=windows golangci-lint run ./...
  ./scripts/check-go-coverage.sh  # at least 85%
  slophammer-go dry . && slophammer-go crap . && slophammer-go check .
  ```
- Keep the Slophammer standards in `slophammer.yml` and `.golangci.yml`. Do not weaken them. A `nolint` needs the rule and a reason, as `nolintlint` requires.
- Tests: the fake llama routers and the fake official llama are the test binary itself (see `TestMain` in each package). Add a test for every behavior change.
- Windows-only code lives in `*_windows.go` files. Keep a Linux version or a clear stub next to it.
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
