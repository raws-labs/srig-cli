package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/raws-labs/srig-cli/client"
	"github.com/raws-labs/srig-cli/firmware"
	"github.com/raws-labs/srig-cli/output"
	"github.com/spf13/cobra"
)

// How often to ask whether a recording has finished.
const recordPoll = 2 * time.Second

func NewRecordCmd(c **client.Client, jsonFlag *bool) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "record <firmware>",
		Short: "Record a run on a real board and get a shareable link and badge",
		Long: "Run firmware on a free board, record the flash, the boot and everything\n" +
			"the board prints for --seconds, and publish it as a run record. Anyone with\n" +
			"the link can play the recording back; the \"Run on SiliconRig\" badge links\n" +
			"to it. The run is a session of yours and costs credits like one.\n\n" +
			"Firmware may be a .bin/.uf2, or an .elf / .hex for STM32 (auto-converted).\n" +
			"--send is written to the board once it has booted and gone quiet.\n\n" +
			"Exit codes: 0 recorded, 1 the recording failed, 2 infrastructure error\n" +
			"(no free board, network, API).",
		Example: "  srig record build/bench.elf --board stm32-h753 --name \"Inference benchmark\"\n" +
			"  srig record demo.bin --board esp32-s3 --send 'selftest\\r' --seconds 20",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			board, _ := cmd.Flags().GetString("board")
			name, _ := cmd.Flags().GetString("name")
			send, _ := cmd.Flags().GetString("send")
			seconds, _ := cmd.Flags().GetInt("seconds")
			timeout, _ := cmd.Flags().GetDuration("timeout")

			data, err := os.ReadFile(args[0])
			if err != nil {
				return fmt.Errorf("cannot read firmware file: %w", err)
			}
			image, info, err := firmware.Normalize(data, board)
			if err != nil {
				return err
			}
			if len(image) > 16<<20 {
				return fmt.Errorf("firmware is %d bytes, over the platform's 16 MB limit", len(image))
			}
			if name == "" {
				name = filepath.Base(args[0])
			}
			sendData := strings.NewReplacer(`\n`, "\n", `\r`, "\r", `\t`, "\t").Replace(send)

			if !*jsonFlag {
				if info.Format != firmware.FormatRaw {
					output.Info(fmt.Sprintf("detected %s, converted to %d-byte bin @ %#x", info.Format, info.Size, info.BaseAddr))
				}
				output.Info(fmt.Sprintf("recording %s on %s", name, board))
			}

			created, err := (*c).CreateRunRecord(board, name, sendData, seconds, "firmware.bin", image)
			if err != nil {
				output.Error(fmt.Sprintf("could not start the recording: %v", err))
				return &ExitError{Code: 2}
			}
			if !*jsonFlag {
				output.Info(fmt.Sprintf("running on %s for %d s", created.BoardLabel, created.RunSeconds))
			}

			if timeout == 0 {
				// The write, the run and the reset with room to spare.
				timeout = time.Duration(created.RunSeconds)*time.Second + 5*time.Minute
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
			defer cancel()
			rec, err := waitForRecord(ctx, *c, created.ID, recordPoll)
			if err != nil {
				output.Error(fmt.Sprintf("record %s: %v", created.ID, err))
				return &ExitError{Code: 2}
			}
			// Only the response that created the record carries these.
			rec.URL, rec.BadgeMarkdown = created.URL, created.BadgeMarkdown

			if *jsonFlag {
				output.JSON(rec)
			} else if rec.State == "ready" {
				output.Success("recorded: " + rec.URL)
				fmt.Println()
				fmt.Println(rec.BadgeMarkdown)
			} else {
				output.Error(fmt.Sprintf("the recording failed: %s (%s)", rec.Error, rec.URL))
			}
			if rec.State != "ready" {
				return &ExitError{Code: 1}
			}
			return nil
		},
	}
	cmd.Flags().String("board", "", "Board type to record on (required)")
	cmd.Flags().String("name", "", "Name shown with the recording (default: the file name)")
	cmd.Flags().String("send", "", "Write this string to the board after it boots (interprets \\n, \\r, \\t)")
	cmd.Flags().Int("seconds", 30, "How long to record (10 to 120)")
	cmd.Flags().Duration("timeout", 0, "Give up waiting after this long (default: the run plus 5 minutes)")
	cmd.MarkFlagRequired("board")
	return cmd
}

// waitForRecord polls a record until it is no longer being recorded.
func waitForRecord(ctx context.Context, c *client.Client, id string, every time.Duration) (*client.RunRecord, error) {
	for {
		rec, err := c.GetRunRecord(id)
		var apiErr *client.APIError
		switch {
		case err == nil && rec.State != "recording":
			return rec, nil
		case err != nil && (!errors.As(err, &apiErr) || apiErr.StatusCode >= 500):
			// A network blip or a coordinator restart: keep asking until the deadline.
		case err != nil:
			return nil, err
		}
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("still recording when the wait ran out")
		case <-time.After(every):
		}
	}
}
