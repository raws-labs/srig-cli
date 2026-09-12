package cmd

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/coder/websocket"
	"github.com/raws-labs/srig-cli/client"
	"github.com/raws-labs/srig-cli/firmware"
	"github.com/raws-labs/srig-cli/output"
	"github.com/raws-labs/srig-cli/runner"
	"github.com/spf13/cobra"
)

// ExitError carries a specific process exit code up to main(). The command has
// already printed all human/JSON output; main only maps the code.
type ExitError struct{ Code int }

func (e *ExitError) Error() string { return fmt.Sprintf("exit status %d", e.Code) }

func NewRunCmd(c **client.Client, jsonFlag *bool) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "run <firmware>",
		Short: "Flash firmware and assert on serial output (one-shot, CI-friendly)",
		Long: "Create a session, flash the firmware, watch serial output, then end the\n" +
			"session — in one command. Firmware may be a .bin/.uf2, or an .elf / .hex\n" +
			"for STM32 (auto-converted).\n\n" +
			"Pass/fail (precedence): a serial line '##srig-exit:N##' sets exit code N;\n" +
			"else --fail matches → exit 1; else --expect must match before --timeout.\n\n" +
			"Use --retries to ride out transient infra failures like no free board\n" +
			"(never retries test failures).\n\n" +
			"Exit codes: 0 pass · 1 test failed/timeout · 2 infrastructure error · 130 interrupted.",
		Example: "  srig run firmware.elf --board stm32-h753 --expect \"All tests passed\"\n" +
			"  srig run app.bin --board esp32-s3 --timeout 90s\n" +
			"  srig run app.bin --board stm32-h753 --expect READY --retries 5 --retry-delay 30s\n" +
			"  srig run firmware.hex --board stm32-f446 --json   # firmware prints ##srig-exit:0##",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			board, _ := cmd.Flags().GetString("board")
			expect, _ := cmd.Flags().GetString("expect")
			failPat, _ := cmd.Flags().GetString("fail")
			timeout, _ := cmd.Flags().GetDuration("timeout")
			retries, _ := cmd.Flags().GetInt("retries")
			retryDelay, _ := cmd.Flags().GetDuration("retry-delay")
			send, _ := cmd.Flags().GetString("send")
			logPath, _ := cmd.Flags().GetString("log")

			if board == "" {
				output.Error("--board is required")
				return &ExitError{Code: 2}
			}
			eval, err := runner.NewEvaluator(expect, failPat)
			if err != nil {
				output.Error(fmt.Sprintf("invalid pattern: %v", err))
				return &ExitError{Code: 2}
			}
			data, err := os.ReadFile(args[0])
			if err != nil {
				output.Error(fmt.Sprintf("cannot read firmware: %v", err))
				return &ExitError{Code: 2}
			}
			flashBytes, fwInfo, err := firmware.Normalize(data, board)
			if err != nil {
				output.Error(err.Error())
				return &ExitError{Code: 2}
			}
			if len(flashBytes) > 16<<20 {
				output.Error(fmt.Sprintf("firmware is %d bytes, over the platform's 16 MB limit", len(flashBytes)))
				return &ExitError{Code: 2}
			}
			if !*jsonFlag && fwInfo.Format != firmware.FormatRaw {
				output.Info(fmt.Sprintf("detected %s, converted to %d-byte bin @ %#x", fwInfo.Format, fwInfo.Size, fwInfo.BaseAddr))
			}

			sendData := strings.NewReplacer(`\n`, "\n", `\r`, "\r", `\t`, "\t").Replace(send)
			var logW io.Writer
			if logPath != "" {
				lf, err := os.Create(logPath)
				if err != nil {
					output.Error(fmt.Sprintf("cannot open log file: %v", err))
					return &ExitError{Code: 2}
				}
				defer lf.Close()
				logW = lf
			}

			// One create→flash→watch attempt; the session is ended on return.
			attempt := func() (runner.Outcome, string, error) {
				sess, err := (*c).CreateSession(board)
				if err != nil {
					return runner.Outcome{}, "", err
				}
				defer func() { _, _ = (*c).EndSession(sess.ID) }()
				o, err := runOnSession(cmd.Context(), *c, sess, flashBytes, eval, timeout, sendData, logW, *jsonFlag)
				return o, sess.ID, err
			}

			var (
				outcome   runner.Outcome
				sessionID string
			)
			for a := 0; ; a++ {
				outcome, sessionID, err = attempt()
				if err == nil {
					break // decided pass/fail
				}
				// Infra error. Retry only transient ones, up to --retries.
				if a >= retries || !client.IsTransient(err) {
					output.Error(err.Error())
					return &ExitError{Code: 2}
				}
				if !*jsonFlag {
					output.Info(fmt.Sprintf("infra error: %v — retrying in %s (%d/%d)", err, retryDelay, a+1, retries))
				}
				select {
				case <-time.After(retryDelay):
				case <-cmd.Context().Done():
					output.Error("cancelled")
					return &ExitError{Code: 130}
				}
			}

			if *jsonFlag {
				output.JSON(runner.ResultFrom(outcome, sessionID))
			} else if outcome.ExitCode == 0 {
				output.Success(fmt.Sprintf("run passed (%s)", outcome.Reason))
			} else {
				output.Error(fmt.Sprintf("run failed: %s (exit %d)", outcome.Reason, outcome.ExitCode))
			}
			if outcome.ExitCode == 0 {
				return nil
			}
			return &ExitError{Code: outcome.ExitCode}
		},
	}
	cmd.Flags().String("board", "", "Board type (required): stm32-h753, stm32-f446, esp32-s3, rp2350")
	cmd.Flags().String("expect", "", "Regex that must appear in serial output for success")
	cmd.Flags().String("fail", "", "Regex that fails the run immediately if it appears")
	cmd.Flags().Duration("timeout", 60*time.Second, "How long to watch serial output after flashing")
	cmd.Flags().Int("retries", 0, "Retry on transient infra failures (e.g. no free board); 0 = no retry")
	cmd.Flags().Duration("retry-delay", 15*time.Second, "Delay between retries")
	cmd.Flags().String("send", "", "Write this string to the board's UART after boot (interprets \\n, \\r, \\t)")
	cmd.Flags().String("log", "", "Save the full serial capture to this file")
	return cmd
}

// runOnSession flashes over the session WS, then watches serial until a terminal
// line, the timeout, or an interrupt. Returned error means an infrastructure
// failure (exit 2); a non-nil Outcome means a decided pass/fail.
func runOnSession(parent context.Context, c *client.Client, sess *client.Session, flashBytes []byte, eval *runner.Evaluator, timeout time.Duration, send string, logW io.Writer, jsonFlag bool) (runner.Outcome, error) {
	if parent == nil {
		parent = context.Background()
	}

	// First: flash (generous fixed timeout, independent of --timeout).
	flashCtx, cancelFlash := context.WithTimeout(parent, 2*time.Minute)
	defer cancelFlash()
	ws, err := c.DialSerialWS(flashCtx, sess.ID)
	if err != nil {
		if parent.Err() != nil {
			return runner.Outcome{ExitCode: 130, Reason: "interrupted"}, nil
		}
		return runner.Outcome{}, fmt.Errorf("connect to session: %w", err)
	}
	defer ws.CloseNow()
	if err := c.FlashFirmwareBytes(sess.ID, "firmware.bin", flashBytes); err != nil {
		if parent.Err() != nil {
			return runner.Outcome{ExitCode: 130, Reason: "interrupted"}, nil
		}
		return runner.Outcome{}, fmt.Errorf("upload firmware: %w", err)
	}
	if err := waitFlashDone(flashCtx, ws, jsonFlag); err != nil {
		if parent.Err() != nil {
			return runner.Outcome{ExitCode: 130, Reason: "interrupted"}, nil
		}
		return runner.Outcome{}, err
	}

	// Optionally drive the board: write to its UART once watching begins.
	if send != "" {
		b64 := base64.StdEncoding.EncodeToString([]byte(send))
		m, _ := json.Marshal(map[string]string{"type": "serial_data", "data": b64})
		wctx, wcancel := context.WithTimeout(parent, 5*time.Second)
		_ = ws.Write(wctx, websocket.MessageText, m)
		wcancel()
	}

	// Then: watch serial for --timeout. The parent context cancels on SIGINT
	// (wired via signal.NotifyContext in main), so a cancelled parent = interrupt.
	watchCtx, cancelWatch := context.WithTimeout(parent, timeout)
	defer cancelWatch()

	var buf strings.Builder
	for {
		_, raw, err := ws.Read(watchCtx)
		if err != nil {
			if parent.Err() != nil {
				return runner.Outcome{ExitCode: 130, Reason: "interrupted"}, nil
			}
			if watchCtx.Err() == context.DeadlineExceeded {
				return eval.Timeout(), nil
			}
			return runner.Outcome{}, fmt.Errorf("connection lost: %w", err)
		}
		var msg map[string]any
		if json.Unmarshal(raw, &msg) != nil {
			continue
		}
		if t, _ := msg["type"].(string); t != "serial_data" {
			continue
		}
		b64, _ := msg["data"].(string)
		decoded, derr := base64.StdEncoding.DecodeString(b64)
		if derr != nil {
			continue
		}
		if jsonFlag {
			os.Stderr.Write(decoded) // keep stdout pure JSON under --json
		} else {
			os.Stdout.Write(decoded)
		}
		if logW != nil {
			logW.Write(decoded)
		}
		buf.Write(decoded)
		for {
			s := buf.String()
			nl := strings.IndexByte(s, '\n')
			if nl < 0 {
				break
			}
			line := s[:nl]
			buf.Reset()
			buf.WriteString(s[nl+1:])
			if o, done := eval.Feed(line); done {
				return o, nil
			}
		}
	}
}

// waitFlashDone consumes flash_output/flash_done frames until the flash finishes.
func waitFlashDone(ctx context.Context, ws wsReader, jsonFlag bool) error {
	for {
		_, raw, err := ws.Read(ctx)
		if err != nil {
			return fmt.Errorf("lost connection while flashing: %w", err)
		}
		var msg map[string]any
		if json.Unmarshal(raw, &msg) != nil {
			continue
		}
		switch msg["type"] {
		case "flash_done":
			if ok, _ := msg["success"].(bool); ok {
				if !jsonFlag {
					output.Info("flash complete")
				}
				return nil
			}
			errMsg, _ := msg["error"].(string)
			return fmt.Errorf("flash failed: %s", errMsg)
		}
	}
}

// wsReader is the subset of *websocket.Conn that waitFlashDone needs.
type wsReader interface {
	Read(ctx context.Context) (websocket.MessageType, []byte, error)
}
