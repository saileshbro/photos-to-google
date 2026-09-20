// Package cli implements ptg's commands.
//
// The output contract is the point of this package: every command writes
// results to stdout one record per line, and everything else (progress,
// summaries, errors) to stderr. So `ptg upload ... | grep ^failed` works, and
// so does `--json`, which switches stdout to NDJSON for a program to read.
package cli

import (
	"errors"
	"fmt"
	"os"

	"github.com/saileshbro/photos-to-google/internal/gotohp/core"
	"github.com/spf13/cobra"
)

// Exit codes. They are part of the interface: a caller branches on these
// rather than parsing messages.
const (
	ExitOK        = 0 // everything asked for succeeded
	ExitFailed    = 1 // the command ran, but one or more items failed
	ExitUsage     = 2 // bad flags, bad input, nothing to do
	ExitAuth      = 3 // no usable credentials
	ExitInterrupt = 130
)

// UsageError makes Main exit with ExitUsage instead of ExitFailed.
type UsageError struct{ error }

func usageErrorf(format string, a ...any) error {
	return UsageError{fmt.Errorf(format, a...)}
}

// AuthError makes Main exit with ExitAuth.
type AuthError struct{ error }

// globalOptions are flags every command accepts.
type globalOptions struct {
	json    bool
	quiet   bool
	config  string
	account string
}

var global globalOptions

func Main(args []string) int {
	root := newRootCommand()
	root.SetArgs(args)
	err := root.Execute()
	switch {
	case err == nil:
		return ExitOK
	case errors.Is(err, errSomeFailed):
		return ExitFailed
	}
	var usage UsageError
	var auth AuthError
	fmt.Fprintln(os.Stderr, "ptg: "+err.Error())
	switch {
	case errors.As(err, &usage):
		return ExitUsage
	case errors.As(err, &auth):
		return ExitAuth
	}
	return ExitFailed
}

// errSomeFailed is returned when a run completed but some items failed. It is
// reported through the normal result lines, so Main stays quiet about it.
var errSomeFailed = errors.New("some items failed")

func newRootCommand() *cobra.Command {
	root := &cobra.Command{
		Use:   "ptg",
		Short: "Upload photos and videos to Google Photos at original quality",
		Long: `ptg uploads photos and videos to Google Photos at original quality,
keeping Apple Live Photos as a single item.

Output: one tab-separated record per line on stdout, progress and errors on
stderr, so results can be piped. Use --json for NDJSON instead.

  ptg upload ~/Pictures/export -r
  find . -name '*.HEIC' -print0 | ptg upload -0 -
  ptg upload --json ~/photos | jq -r 'select(.status=="failed") | .path'`,
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			if err := core.LoadConfig(global.config); err != nil && global.config != "" {
				return usageErrorf("cannot read config %s: %v", global.config, err)
			}
			return nil
		},
	}
	f := root.PersistentFlags()
	f.BoolVar(&global.json, "json", false, "write NDJSON records to stdout instead of tab-separated lines")
	f.BoolVarP(&global.quiet, "quiet", "q", false, "suppress progress and summary on stderr")
	f.StringVar(&global.config, "config", "", "path to the config file holding credentials")
	f.StringVar(&global.account, "account", "", "Google account email to use (default: the selected one)")

	root.AddCommand(newAuthCommand(), newUploadCommand(), newPhotosCommand(), newVersionCommand())
	return root
}

func newVersionCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the version",
		RunE: func(cmd *cobra.Command, _ []string) error {
			out := newEmitter(cmd.OutOrStdout())
			return out.record(map[string]any{"event": "version", "version": Version},
				"version\t"+Version)
		},
	}
}

// Version is set at build time with -ldflags "-X ...cli.Version=x.y.z".
var Version = "dev"

// asUsage reports whether err is a UsageError, for tests and callers that need
// to branch before Main maps it to an exit code.
func asUsage(err error, target *UsageError) bool {
	return errors.As(err, target)
}
