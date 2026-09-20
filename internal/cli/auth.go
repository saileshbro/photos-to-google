package cli

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"github.com/saileshbro/photos-to-google/internal/gotohp/core"
	"github.com/spf13/cobra"
)

const signInHelp = `Sign in once, then ptg keeps the credential:

  1. Open https://accounts.google.com/EmbeddedSetup and sign in.
     The page may keep loading after you sign in; that is expected.
  2. Copy the value of the "oauth_token" cookie (DevTools → Application →
     Cookies). It starts with "oauth2_4/", is single-use, and expires within
     minutes.
  3. ptg auth login --clipboard      (or: ptg auth login "<token>", or pipe it)

The token is exchanged for a long-lived credential and is never printed.`

func newAuthCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "auth",
		Short: "Manage Google account credentials",
		Long:  signInHelp,
	}
	cmd.AddCommand(newAuthLoginCommand(), newAuthStatusCommand(), newAuthLogoutCommand())
	return cmd
}

func newAuthLoginCommand() *cobra.Command {
	var fromClipboard bool
	cmd := &cobra.Command{
		Use:   "login [TOKEN|-]",
		Short: "Add a Google account from an Embedded Setup oauth_token",
		Long: signInHelp + `

A raw credential string (androidId=...&Email=...) is accepted too, so a
credential captured elsewhere can be imported.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			token, err := readToken(cmd.InOrStdin(), args, fromClipboard)
			if err != nil {
				return err
			}
			var mgr core.ConfigManager
			email, err := mgr.AddGoogleAccount(token)
			if err != nil {
				return AuthError{fmt.Errorf("sign-in failed: %w (the oauth_token is single-use and short-lived — copy a fresh one)", err)}
			}
			mgr.SetSelected(email)
			out := newEmitter(cmd.OutOrStdout())
			return out.record(
				map[string]any{"event": "login", "account": email, "config": core.ConfigPath},
				tabs("logged-in", email, core.ConfigPath))
		},
	}
	cmd.Flags().BoolVar(&fromClipboard, "clipboard", false, "read the token from the clipboard and clear it afterwards")
	return cmd
}

// readToken takes the token from an argument, the clipboard, or stdin. It never
// prompts: a command that blocks on a TTY is useless inside an agent or a pipe.
func readToken(stdin io.Reader, args []string, fromClipboard bool) (string, error) {
	switch {
	case fromClipboard:
		if runtime.GOOS != "darwin" {
			return "", usageErrorf("--clipboard is macOS only; pass the token as an argument or on stdin")
		}
		b, err := exec.Command("pbpaste").Output()
		if err != nil {
			return "", usageErrorf("cannot read the clipboard: %v", err)
		}
		_ = exec.Command("sh", "-c", "printf '' | pbcopy").Run() // clear it either way
		return validToken(string(b))
	case len(args) == 1 && args[0] != "-":
		return validToken(args[0])
	case len(args) == 1 || !isTerminal(os.Stdin):
		b, err := io.ReadAll(bufio.NewReader(stdin))
		if err != nil {
			return "", usageErrorf("cannot read the token from stdin: %v", err)
		}
		return validToken(string(b))
	}
	return "", usageErrorf("no token given\n\n%s", signInHelp)
}

func validToken(s string) (string, error) {
	s = strings.TrimSpace(s)
	switch {
	case s == "":
		return "", usageErrorf("the token is empty\n\n%s", signInHelp)
	case strings.HasPrefix(s, "oauth2_4/"), core.LooksLikeAuthString(s):
		return s, nil
	}
	return "", usageErrorf("that does not look like an oauth_token (expected it to start with \"oauth2_4/\") or a credential string\n\n%s", signInHelp)
}

func newAuthStatusCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "List the accounts ptg can upload with",
		RunE: func(cmd *cobra.Command, _ []string) error {
			var mgr core.ConfigManager
			state := mgr.GetAccounts()
			out := newEmitter(cmd.OutOrStdout())
			for _, a := range state.Accounts {
				selected := "no"
				if a.Email == state.Selected {
					selected = "yes"
				}
				if err := out.record(
					map[string]any{"event": "account", "account": a.Email, "selected": a.Email == state.Selected,
						"needs_token_binding": a.NeedsTokenBinding},
					tabs("account", a.Email, "selected="+selected)); err != nil {
					return err
				}
			}
			if len(state.Accounts) == 0 {
				return AuthError{fmt.Errorf("no accounts; run: ptg auth login --clipboard")}
			}
			return nil
		},
	}
}

func newAuthLogoutCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "logout [EMAIL]",
		Short: "Remove a stored credential",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var mgr core.ConfigManager
			state := mgr.GetAccounts()
			email := state.Selected
			if len(args) == 1 {
				email = args[0]
			}
			if email == "" {
				return usageErrorf("no account to remove")
			}
			if err := mgr.RemoveCredentials(email); err != nil {
				return fmt.Errorf("cannot remove %s: %w", email, err)
			}
			out := newEmitter(cmd.OutOrStdout())
			return out.record(map[string]any{"event": "logout", "account": email}, tabs("logged-out", email))
		},
	}
}
