package cmd

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/coder/websocket"
	"github.com/raws-labs/srig-cli/client"
	"github.com/raws-labs/srig-cli/output"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

func NewSerialCmd(c **client.Client, jsonFlag *bool) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "serial",
		Short: "Open serial terminal to a remote board",
		Long:  "Attach to the serial console of the board in your active session.\nPress Ctrl+] to disconnect. If --timeout is set, exits after the duration.\nUse --log to save serial output to a file.",
		RunE: func(cmd *cobra.Command, args []string) error {
			sessionID, _ := cmd.Flags().GetString("session")
			timeout, _ := cmd.Flags().GetDuration("timeout")
			logFile, _ := cmd.Flags().GetString("log")

			// Find session
			var sess *client.Session
			var err error
			if sessionID != "" {
				sess, err = (*c).GetSession(sessionID)
				if err != nil {
					return fmt.Errorf("get session: %w", err)
				}
			} else {
				sess, err = (*c).FindActiveSession()
				if err != nil {
					return err
				}
			}

			// provisioning is allowed: the base image write streams into this
			// console, so attaching during it shows what the board is doing.
			if sess.State != "active" && sess.State != "idle" && sess.State != "provisioning" {
				return fmt.Errorf("session %s is %s, not active", sess.ID, sess.State)
			}

			// Set up context
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			if timeout > 0 {
				ctx, cancel = context.WithTimeout(ctx, timeout)
				defer cancel()
			}

			// Handle signals
			sigCh := make(chan os.Signal, 1)
			signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
			go func() {
				<-sigCh
				cancel()
			}()

			// Connect WebSocket
			ws, err := (*c).DialSerialWS(ctx, sess.ID)
			if err != nil {
				return err
			}
			defer ws.CloseNow()

			// Open log file if requested
			var logWriter *os.File
			if logFile != "" {
				logWriter, err = os.Create(logFile)
				if err != nil {
					return fmt.Errorf("open log file: %w", err)
				}
				defer logWriter.Close()
			}

			if !*jsonFlag {
				msg := fmt.Sprintf("Connected to %s (%s). Press Ctrl+] to disconnect.", sess.BoardType, sess.ID[:8])
				if logWriter != nil {
					msg += fmt.Sprintf(" Logging to %s.", logFile)
				}
				fmt.Fprintln(os.Stderr, msg)
			}

			// Put terminal in raw mode for interactive use
			stdinFd := int(os.Stdin.Fd())
			if term.IsTerminal(stdinFd) {
				oldState, err := term.MakeRaw(stdinFd)
				if err == nil {
					defer term.Restore(stdinFd, oldState)
				}
			}

			errCh := make(chan error, 2)

			// Read from WS → stdout
			go func() {
				for {
					_, data, err := ws.Read(ctx)
					if err != nil {
						errCh <- err
						return
					}
					var msg map[string]any
					if json.Unmarshal(data, &msg) != nil {
						continue
					}
					if t, _ := msg["type"].(string); t == "serial_data" {
						b64, _ := msg["data"].(string)
						raw, err := base64.StdEncoding.DecodeString(b64)
						if err == nil {
							os.Stdout.Write(raw)
							if logWriter != nil {
								logWriter.Write(raw)
							}
						}
					}
				}
			}()

			// Read from stdin → WS
			go func() {
				buf := make([]byte, 1024)
				for {
					n, err := os.Stdin.Read(buf)
					if err != nil {
						if err != io.EOF {
							errCh <- err
						}
						return
					}

					// Ctrl+] (0x1d) = disconnect
					for i := 0; i < n; i++ {
						if buf[i] == 0x1d {
							if !*jsonFlag {
								fmt.Fprintf(os.Stderr, "\r\nDisconnected.\r\n")
							}
							cancel()
							return
						}
					}

					b64 := base64.StdEncoding.EncodeToString(buf[:n])
					msg, _ := json.Marshal(map[string]string{
						"type": "serial_data",
						"data": b64,
					})
					writeCtx, writeCancel := context.WithTimeout(ctx, 5*time.Second)
					err = ws.Write(writeCtx, websocket.MessageText, msg)
					writeCancel()
					if err != nil {
						errCh <- err
						return
					}
				}
			}()

			// Wait for context cancellation or error
			select {
			case <-ctx.Done():
				if ctx.Err() == context.DeadlineExceeded && !*jsonFlag {
					fmt.Fprintf(os.Stderr, "\r\nTimeout reached.\r\n")
				}
			case err := <-errCh:
				if ctx.Err() == nil && !*jsonFlag {
					output.Error(fmt.Sprintf("connection lost: %v", err))
				}
			}

			ws.Close(websocket.StatusNormalClosure, "closing")
			return nil
		},
	}

	cmd.Flags().String("session", "", "Session ID (auto-detects active session if omitted)")
	cmd.Flags().Duration("timeout", 0, "Auto-disconnect after duration (0 = no timeout, e.g. 30s for CI)")
	cmd.Flags().String("log", "", "Save serial output to file")
	return cmd
}
