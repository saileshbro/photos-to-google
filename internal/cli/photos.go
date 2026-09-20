package cli

import "github.com/spf13/cobra"

// newPhotosCommand is the Apple Photos library source. It is a placeholder
// until `photos list` and `photos export` land.
func newPhotosCommand() *cobra.Command {
	return &cobra.Command{
		Use:    "photos",
		Short:  "Work with the macOS Photos library (coming next)",
		Hidden: true,
	}
}
