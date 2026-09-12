# srig-cli

Command-line client for SiliconRig (siliconrig.dev): flash firmware, open a serial terminal, and run CI tests on real embedded hardware. Go (version in `go.mod`) with cobra, lipgloss, coder/websocket. Public, github.com/raws-labs/srig-cli, Apache-2.0.

## Build, test, run
- `go build -o srig .` then `./srig --help`.
- `go test ./...`: unit tests (client, firmware, runner).
- Release: push a `v*` tag; the GitHub Actions workflow runs goreleaser (linux/darwin/windows; amd64/arm64/armv7) and stamps `-X main.Version`. End users install with `curl -fsSL https://siliconrig.dev/install.sh | sh` or from GitHub Releases.
- `go install github.com/raws-labs/srig-cli@latest` also works but yields a binary named `srig-cli` that reports version `dev`.

## Layout
- `main.go`: cobra root, global flags, signal handling, exit-code mapping.
- `cmd/`: one file per command (status, session, flash, run, serial, power, whoami).
- `client/`: HTTP client and API types; `IsTransient` classifies retryable errors.
- `firmware/`: ELF and Intel HEX to raw-image conversion. It is an exported package with external consumers; treat its API as stable and version breaking changes.
- `runner/`: serial evaluation for `srig run` (expect/fail regexes, exit sentinel). `output/`: table, JSON, and error rendering.

## Conventions
- Global flags: `--api-key` (`SRIG_API_KEY`), `--base-url` (`SRIG_BASE_URL`, default `https://api.srig.io`), `--json`.
- API keys start with `key_`; every endpoint lives under `/v1/`.
- `srig run` exit codes: 0 pass, 1 test failed or timeout, 2 infrastructure error, 130 interrupted. A serial line matching `^##srig-exit:N##` sets the exit code directly. `--retries` retries infra errors only, never test failures.
- `srig flash` accepts raw `.bin` (all boards), `.uf2` (rp2350), and `.elf`/`.hex` for STM32 boards, converted client-side; STM32 images must be linked at `0x08000000`.
- `srig session end` with no ID ends the active session. `srig serial` exits on Ctrl+].

## Gotchas
- The module path became `github.com/raws-labs/srig-cli` at v0.4.0; `go install` of the old path fails with "module declares its path as ...", while pinned pre-0.4.0 versions still resolve.
