package cli

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/saileshbro/photos-to-google/internal/photoslib"
	"github.com/spf13/cobra"
)

func newPhotosCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "photos",
		Short: "Use the macOS Photos library as a source",
		Long: `Read the macOS Photos library.

"photos list" reports what the library holds. "photos export" writes the files
out, including each edited version and the video half of every Live Photo, and
prints their paths so they can be piped into "ptg upload".`,
	}
	cmd.AddCommand(newPhotosListCommand(), newPhotosExportCommand())
	return cmd
}

type photoFilterFlags struct {
	library  string
	from, to string
	uuids    []string
	video    bool
	photo    bool
	live     bool
	edited   bool
	limit    int
}

func (p photoFilterFlags) filter() (photoslib.Filter, error) {
	f := photoslib.Filter{UUIDs: p.uuids, VideoOnly: p.video, PhotoOnly: p.photo,
		LiveOnly: p.live, EditsOnly: p.edited, Limit: p.limit}
	var err error
	if p.from != "" {
		if f.From, err = time.Parse(time.DateOnly, p.from); err != nil {
			return f, usageErrorf("--from must be a date like 2025-10-01")
		}
	}
	if p.to != "" {
		if f.To, err = time.Parse(time.DateOnly, p.to); err != nil {
			return f, usageErrorf("--to must be a date like 2025-10-31")
		}
		f.To = f.To.AddDate(0, 0, 1) // --to is inclusive
	}
	return f, nil
}

func (p *photoFilterFlags) register(cmd *cobra.Command) {
	f := cmd.Flags()
	f.StringVar(&p.library, "library", "", "Photos library to read (default: the system library)")
	f.StringVar(&p.from, "from", "", "only items taken on or after this date (2025-10-01)")
	f.StringVar(&p.to, "to", "", "only items taken on or before this date (2025-10-31)")
	f.StringArrayVar(&p.uuids, "uuid", nil, "only this item; repeatable")
	f.BoolVar(&p.video, "video", false, "only videos")
	f.BoolVar(&p.photo, "photo", false, "only photos")
	f.BoolVar(&p.live, "live", false, "only Live Photos")
	f.BoolVar(&p.edited, "edited", false, "only items you have edited")
	f.IntVar(&p.limit, "limit", 0, "stop after this many items")
}

func newPhotosListCommand() *cobra.Command {
	var p photoFilterFlags
	var paths bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List items in the Photos library",
		Long: `List the visible items in the library: one record per line, oldest first.

  UUID<TAB>TAKEN<TAB>FILENAME<TAB>FLAGS<TAB>ORIGINAL_PATH

FLAGS is a comma-separated set of video, live and edited. Hidden items, items in
Recently Deleted, and unpicked burst frames are left out, which is what Photos
counts too.

With --paths it prints only the path of each original file, so the library can
be piped straight into upload. An original is the unedited file: to send your
edits as well, use "ptg photos export".

  ptg photos list --from 2025-10-01 --live
  ptg photos list --paths --video | ptg upload -
  ptg photos list --json | jq -r 'select(.is_edited) | .uuid'`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			filter, err := p.filter()
			if err != nil {
				return err
			}
			lib, err := photoslib.Open(p.library)
			if err != nil {
				return usageErrorf("%v", err)
			}
			defer lib.Close()
			items, err := lib.Items(filter)
			if err != nil {
				return err
			}
			out := newEmitter(cmd.OutOrStdout())
			for _, it := range items {
				if paths {
					if it.Original == "" {
						continue
					}
					if err := out.record(map[string]any{"event": "path", "path": it.Original}, it.Original); err != nil {
						return err
					}
					continue
				}
				if err := out.record(
					map[string]any{"event": "item", "uuid": it.UUID, "taken": it.Taken.Format(time.RFC3339),
						"filename": it.Filename, "is_video": it.IsVideo, "is_live": it.IsLive,
						"is_edited": it.IsEdited, "original": it.Original, "size_bytes": it.SizeBytes},
					tabs(it.UUID, it.Taken.Format(time.DateTime), it.Filename, flagsOf(it), it.Original)); err != nil {
					return err
				}
			}
			out.summary(map[string]any{"event": "summary", "items": len(items), "library": lib.Path()},
				fmt.Sprintf("%d items in %s", len(items), lib.Path()))
			return nil
		},
	}
	p.register(cmd)
	cmd.Flags().BoolVar(&paths, "paths", false, "print only the path of each original file")
	return cmd
}

func flagsOf(it photoslib.Item) string {
	var f []string
	if it.IsVideo {
		f = append(f, "video")
	}
	if it.IsLive {
		f = append(f, "live")
	}
	if it.IsEdited {
		f = append(f, "edited")
	}
	if len(f) == 0 {
		return "-"
	}
	return strings.Join(f, ",")
}

func newPhotosExportCommand() *cobra.Command {
	var p photoFilterFlags
	var album string
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "export DIR",
		Short: "Export library items to files, ready to upload",
		Long: `Export items from the Photos library into DIR, then print the path of every
file written, one per line.

Each item exports as its original. An item you edited also exports its edited
version, and a Live Photo also exports its video. That naming is what lets
upload pair a Live Photo back into one item, so export and upload belong
together:

  ptg photos export ~/export --from 2025-01-01 | ptg upload -

The date Photos shows is written into each file, so Google Photos places the
item on the day it was taken rather than the day it was uploaded. Re-running
skips files already written, so an interrupted export is safe to repeat.

This runs osxphotos (uv tool install osxphotos) and exiftool
(brew install exiftool).`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := args[0]
			bin, err := exec.LookPath("osxphotos")
			if err != nil {
				return usageErrorf("osxphotos is not installed: run `uv tool install osxphotos` (or pip install osxphotos)")
			}
			if _, err := exec.LookPath("exiftool"); err != nil {
				return usageErrorf("exiftool is not installed: run `brew install exiftool`")
			}
			osxArgs := []string{"export", dir,
				"--directory", "{created.year}/{created.mm}",
				"--skip-bursts", // unpicked burst frames are not items Photos counts
				"--exiftool",    // write the date Photos shows into the file itself
				"--touch-file",
				"--update", // skip what is already exported
				"--no-progress",
			}
			if p.library != "" {
				osxArgs = append(osxArgs, "--library", p.library)
			}
			if album != "" {
				osxArgs = append(osxArgs, "--album", album)
			}
			if p.from != "" {
				osxArgs = append(osxArgs, "--from-date", p.from)
			}
			if p.to != "" {
				osxArgs = append(osxArgs, "--to-date", p.to)
			}
			for _, u := range p.uuids {
				osxArgs = append(osxArgs, "--uuid", u)
			}
			if p.video {
				osxArgs = append(osxArgs, "--only-movies")
			}
			if p.photo {
				osxArgs = append(osxArgs, "--only-photos")
			}
			if p.limit > 0 {
				osxArgs = append(osxArgs, "--limit", strconv.Itoa(p.limit))
			}
			if dryRun {
				osxArgs = append(osxArgs, "--dry-run")
			}

			out := newEmitter(cmd.OutOrStdout())
			out.progress(map[string]any{"event": "export_start", "dir": dir},
				"exporting with osxphotos (this can take a while)…")

			// osxphotos writes for people, so its output goes to stderr and its
			// export database becomes the record of what was written.
			run := exec.CommandContext(cmd.Context(), bin, osxArgs...)
			run.Stdout = os.Stderr
			run.Stderr = os.Stderr
			if err := run.Run(); err != nil {
				return fmt.Errorf("osxphotos export failed: %w", err)
			}
			if dryRun {
				out.summary(map[string]any{"event": "summary", "dry_run": true, "dir": dir},
					"dry run: osxphotos reported the above; nothing was written")
				return nil
			}
			files, err := photoslib.ExportedFiles(dir)
			if err != nil {
				return err
			}
			for _, f := range files {
				if err := out.record(map[string]any{"event": "exported", "path": f}, f); err != nil {
					return err
				}
			}
			out.summary(map[string]any{"event": "summary", "exported_files": len(files), "dir": dir},
				fmt.Sprintf("%d files in %s", len(files), dir))
			return nil
		},
	}
	p.register(cmd)
	cmd.Flags().StringVar(&album, "album", "", "only items in this album")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "let osxphotos report what it would export, and write nothing")
	return cmd
}
