package cmd

import (
	"errors"
	"fmt"

	"github.com/raws-labs/srig-cli/client"
	"github.com/raws-labs/srig-cli/output"
	"github.com/spf13/cobra"
)

func NewSessionCmd(c **client.Client, jsonFlag *bool, baseURL *string) *cobra.Command {
	sessionCmd := &cobra.Command{
		Use:   "session",
		Short: "Manage sessions (create, list, end)",
	}

	createCmd := &cobra.Command{
		Use:   "create",
		Short: "Create a new session",
		RunE: func(cmd *cobra.Command, args []string) error {
			board, _ := cmd.Flags().GetString("board")
			if board == "" {
				return fmt.Errorf("--board is required")
			}

			sess, err := (*c).CreateSession(board)
			if err != nil {
				var apiErr *client.APIError
				if errors.As(err, &apiErr) {
					switch apiErr.StatusCode {
					case 402:
						return fmt.Errorf("insufficient credits — add credits at %s", *baseURL)
					case 409:
						return fmt.Errorf("you already have an active session — end it first")
					case 503:
						return fmt.Errorf("%s", apiErr.Message)
					}
				}
				return err
			}

			if *jsonFlag {
				output.JSON(sess)
				return nil
			}

			rows := [][2]string{
				{"id", sess.ID},
				{"board", sess.BoardType},
				{"state", sess.State},
			}
			// A session with nothing attached to its serial console ends on its
			// own, which is what catches people running these commands one at a
			// time for the first time.
			if cfg, err := (*c).GetConfig(); err == nil && cfg.IdleTimeoutSeconds > 0 {
				rows = append(rows, [2]string{"ends after", fmt.Sprintf("%ds idle", cfg.IdleTimeoutSeconds)})
			}
			output.Card("session created", rows)
			output.Info("flash or open the serial console to keep it alive, or use `srig run` to do both in one command")
			return nil
		},
	}
	createCmd.Flags().String("board", "", "Board type: esp32-s3, stm32-h753, stm32-f446, rp2350")

	listCmd := &cobra.Command{
		Use:   "list",
		Short: "List your sessions",
		RunE: func(cmd *cobra.Command, args []string) error {
			page, err := (*c).ListSessions()
			if err != nil {
				return err
			}

			if *jsonFlag {
				output.JSON(page)
				return nil
			}

			// Combine active + ended for display
			all := append(page.Active, page.Ended...)
			headers := []string{"ID", "BOARD", "STATE", "CREDITS", "CREATED"}
			rows := make([][]string, len(all))
			for i, s := range all {
				rows[i] = []string{
					s.ID,
					s.BoardType,
					s.State,
					fmt.Sprintf("%d", s.CreditsUsed),
					s.CreatedAt,
				}
			}

			output.Table(headers, rows)
			return nil
		},
	}

	endCmd := &cobra.Command{
		Use:   "end [session-id]",
		Short: "End a session",
		Long:  "End a session by ID. If no ID is given, ends the most recent active session.",
		RunE: func(cmd *cobra.Command, args []string) error {
			var id string

			if len(args) > 0 {
				id = args[0]
			} else {
				sess, err := (*c).FindActiveSession()
				if err == nil {
					id = sess.ID
				}
				if id == "" {
					return fmt.Errorf("no active session found")
				}
			}

			sess, err := (*c).EndSession(id)
			if err != nil {
				return err
			}

			if *jsonFlag {
				output.JSON(sess)
				return nil
			}

			output.Card("session ended", [][2]string{
				{"id", sess.ID},
				{"credits", fmt.Sprintf("%d used", sess.CreditsUsed)},
			})
			return nil
		},
	}

	sessionCmd.AddCommand(createCmd)
	sessionCmd.AddCommand(listCmd)
	sessionCmd.AddCommand(endCmd)
	return sessionCmd
}
