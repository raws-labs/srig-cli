# srig-cli

CLI tool for the [SiliconRig](https://siliconrig.dev) Hardware-as-a-Service platform.

Flash firmware, open serial terminals, and run CI/CD tests on real embedded boards, all from your terminal.

## Install

```bash
curl -fsSL https://siliconrig.dev/install.sh | sh
```

Or download a binary from [Releases](https://github.com/raws-labs/srig-cli/releases), or build from source:

```bash
go install github.com/raws-labs/srig-cli@latest
```

## Authentication

```bash
export SRIG_API_KEY=key_...
```

Or pass `--api-key` to any command.

## First run

No toolchain, and no board on your desk:

```bash
curl -LO https://github.com/raws-labs/srig-examples/releases/latest/download/demo-shell-esp32-s3.bin
srig run demo-shell-esp32-s3.bin --board esp32-s3 --expect "0 failed"
```

`srig run` reserves a board, flashes it, watches the serial output for the
pattern, ends the session, and exits 0 on a match. That exit code is the whole
CI integration.

## Commands

```
srig status                        Show board availability
srig session create --board TYPE   Start a session
srig session list                  List your sessions
srig session end [ID]              End a session (auto-detects active)
srig flash <firmware>              Flash firmware to the board
srig run <firmware> --board TYPE   Flash, watch serial, and assert (one-shot, CI-ready exit code)
srig serial                        Open serial terminal (Ctrl+] to exit)
srig power-cycle                   Power cycle the board
srig whoami                        Show your account info
srig version                       Print CLI version
```

`srig flash` accepts a raw `.bin` (all boards), a `.uf2` (rp2350), or, for
STM32 boards, an `.elf` / Intel `.hex`, auto-converted client-side to a raw
image before upload (must be linked at the `0x08000000` flash base).

### `srig run`

Create a session, flash, watch serial output for a pass/fail condition, then
end the session, all in one command, with an exit code your CI can act on
directly:

```bash
srig run firmware.bin --board esp32-s3 --expect "All tests passed"
```

A serial line `##srig-exit:N##` sets the exit code to `N` directly; otherwise
`--fail` (regex) fails the run immediately, and `--expect` (regex) must match
before `--timeout` (default `60s`). Exit codes: `0` pass, `1` test
failed/timeout, `2` infrastructure error, `130` interrupted. Use `--retries`
/ `--retry-delay` to ride out transient infra failures (e.g. no free board).
Test failures are never retried. See `srig run --help` for all flags
(`--send`, `--log`, `--json`, etc.).

## Flags

| Flag | Env | Description |
|------|-----|-------------|
| `--api-key` | `SRIG_API_KEY` | API key |
| `--base-url` | `SRIG_BASE_URL` | API base URL (default: `https://api.srig.io`) |
| `--json` | | JSON output for scripting/CI |

## CI/CD Usage

```yaml
- name: Flash and test
  env:
    SRIG_API_KEY: ${{ secrets.SRIG_API_KEY }}
  run: |
    srig session create --board esp32-s3
    srig flash firmware.bin
    srig serial --timeout 30s --log output.txt
    srig session end
```
