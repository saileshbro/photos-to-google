package cli

import (
	"bufio"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/saileshbro/photos-to-google/internal/gotohp/core"
	"github.com/spf13/cobra"
)

// Result statuses. These strings are the first field of every result line and
// the "status" of every NDJSON result record, so they are API.
const (
	statusUploaded = "uploaded" // new item in Google Photos
	statusExists   = "exists"   // identical bytes were already there
	statusSkipped  = "skipped"  // deliberately not uploaded; see the reason
	statusFailed   = "failed"   // Google refused it or the transfer broke
)

type uploadOptions struct {
	recursive      bool
	threads        int
	album          string
	albumAuto      bool
	force          bool
	dryRun         bool
	saver          bool
	useQuota       bool
	exclude        string
	nullSep        bool
	retries        int
	pairLive       bool
	skipIncomplete bool
	updateToLive   bool
	dateFromName   bool
	serve          string
}

func newUploadCommand() *cobra.Command {
	var o uploadOptions
	cmd := &cobra.Command{
		Use:   "upload [PATH...|-]",
		Short: "Upload files, folders, or a list of paths from stdin",
		Long: `Upload files and videos to Google Photos at original quality.

PATH may be a file or a folder (add -r to walk it). A PATH of "-" reads paths
from stdin, one per line, or NUL-separated with -0. Unsupported file types are
skipped unless --no-filter is set.

Apple Live Photos are uploaded as one item: the still and its .mov are matched
by the content identifier Apple embeds in both, or by filename stem with
--match-by-filename. A pair whose video is already in Google Photos (which
happens when an edited copy of the same Live Photo went up first) is retried as
a still on its own rather than reported as an error.

Every result is one line on stdout:

  STATUS<TAB>PATH<TAB>DETAIL[<TAB>KEY=VALUE...]

STATUS is uploaded, exists, skipped, or failed. DETAIL is the Google Photos
media key for uploaded items and the reason otherwise. Progress and the summary
go to stderr. Exit code 1 means at least one item failed.

Examples:
  ptg upload ~/export -r
  find ~/export -name '*.HEIC' -print0 | ptg upload -0 -
  ptg upload --json ~/export | jq -r 'select(.status=="failed") | .path'
  ptg upload ~/export -r --json | tee upload.ndjson | grep -c '"status":"uploaded"'`,
		RunE: func(cmd *cobra.Command, args []string) error {
			paths, err := resolvePaths(cmd.InOrStdin(), args, o.nullSep)
			if err != nil {
				return err
			}
			return runUpload(cmd, paths, o)
		},
	}
	f := cmd.Flags()
	f.BoolVarP(&o.recursive, "recursive", "r", false, "walk folders recursively")
	f.IntVarP(&o.threads, "threads", "j", 4, "files uploaded at the same time")
	f.StringVarP(&o.album, "album", "a", "", "add uploads to this album, creating it if needed")
	f.BoolVar(&o.albumAuto, "album-auto", false, "create one album per source folder")
	f.BoolVar(&o.force, "force", false, "upload even when identical bytes are already in Google Photos")
	f.BoolVar(&o.dryRun, "dry-run", false, "list what would be uploaded and exit")
	f.BoolVar(&o.saver, "saver", false, "upload in storage-saver quality instead of original")
	f.BoolVar(&o.useQuota, "use-quota", false, "count uploads against the account's storage quota")
	f.StringVar(&o.exclude, "exclude", "", "skip folders whose name matches this pattern")
	f.BoolVarP(&o.nullSep, "null", "0", false, "stdin paths are NUL-separated (pairs with find -print0)")
	f.IntVar(&o.retries, "retries", 2, "extra passes over failed items, with backoff")
	f.BoolVar(&o.pairLive, "pair-live-photos", true, "upload a Live Photo's still and video as one item")
	f.BoolVar(&o.skipIncomplete, "skip-incomplete-live-photos", false, "skip a Live Photo whose other half is missing instead of uploading it alone")
	f.BoolVar(&o.updateToLive, "update-existing-to-live", true, "attach a video to a still already in Google Photos")
	f.BoolVar(&o.dateFromName, "date-from-filename", false, "take the date from the filename (e.g. 20240709_182027.jpg)")
	f.StringVar(&o.serve, "serve", "", "serve a live progress page on this address (e.g. :8765, reachable from a phone)")
	return cmd
}

// resolvePaths collects paths from arguments and, when "-" is given or stdin is
// a pipe, from stdin. Reading stdin only when it is not a terminal keeps
// `ptg upload` from hanging on an empty invocation.
func resolvePaths(stdin io.Reader, args []string, nullSep bool) ([]string, error) {
	var paths []string
	readStdin := false
	for _, a := range args {
		if a == "-" {
			readStdin = true
			continue
		}
		paths = append(paths, a)
	}
	if !readStdin && len(paths) == 0 && !isTerminal(os.Stdin) {
		readStdin = true
	}
	if readStdin {
		sc := bufio.NewScanner(stdin)
		sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
		if nullSep {
			sc.Split(scanNull)
		}
		for sc.Scan() {
			if p := strings.TrimSpace(sc.Text()); p != "" {
				paths = append(paths, p)
			}
		}
		if err := sc.Err(); err != nil {
			return nil, usageErrorf("cannot read paths from stdin: %v", err)
		}
	}
	if len(paths) == 0 {
		return nil, usageErrorf("no paths given; pass files or folders, or pipe a list and use -")
	}
	return paths, nil
}

func scanNull(data []byte, atEOF bool) (int, []byte, error) {
	for i, b := range data {
		if b == 0 {
			return i + 1, data[:i], nil
		}
	}
	if atEOF && len(data) > 0 {
		return len(data), data, nil
	}
	return 0, nil, nil
}

func runUpload(cmd *cobra.Command, paths []string, o uploadOptions) error {
	opts := core.UploadOptions{
		Api:                        core.ApiOptions{Account: global.account, Saver: o.saver, UseQuota: o.useQuota},
		Recursive:                  o.recursive,
		ExcludePattern:             o.exclude,
		Threads:                    o.threads,
		ForceUpload:                o.force,
		SetDateFromFilename:        o.dateFromName,
		PairLivePhotos:             o.pairLive,
		SkipIncompleteLivePhotos:   o.skipIncomplete,
		UpdateExistingPhotosToLive: o.updateToLive,
		AlbumName:                  o.album,
		AlbumAutoMode:              o.albumAuto,
	}
	out := newEmitter(cmd.OutOrStdout())

	if o.dryRun {
		files, err := core.FilterGooglePhotosFiles(paths, opts)
		if err != nil {
			return usageErrorf("cannot read the paths: %v", err)
		}
		for _, p := range files {
			if err := out.record(map[string]any{"event": "result", "status": "would-upload", "path": p},
				tabs("would-upload", p, "")); err != nil {
				return err
			}
		}
		out.summary(map[string]any{"event": "summary", "would_upload": len(files), "dry_run": true},
			fmt.Sprintf("%d files would be uploaded", len(files)))
		return nil
	}
	if _, err := core.NewApi(opts.Api); err != nil {
		return AuthError{fmt.Errorf("%w — run: ptg auth login --clipboard", err)}
	}

	state := newProgressState()
	if o.serve != "" {
		url, err := serveProgress(o.serve, state)
		if err != nil {
			return err
		}
		out.progress(map[string]any{"event": "serving", "url": url}, "progress page: "+url)
	}

	totals := map[string]int{}
	var failedPaths []string
	pass := 0
	for {
		state.setPass(pass)
		results, interrupted := uploadPass(out, state, paths, opts, pass)
		failedPaths = nil
		for _, r := range results {
			status, detail := classify(r)
			totals[status]++
			if status == statusFailed {
				failedPaths = append(failedPaths, r.Paths...)
				if len(r.Paths) == 0 {
					failedPaths = append(failedPaths, r.Path)
				}
			}
			// A pair whose video Google already has: the still still needs a home.
			if r.SkipCode == "remote-live-photo-component-exists" && len(r.Paths) > 1 {
				failedPaths = append(failedPaths, r.Path)
				totals[status]--
				totals["retry-as-still"]++
			}
			_ = detail
		}
		if interrupted {
			return errInterrupted
		}
		pass++
		if len(failedPaths) == 0 || pass > o.retries {
			break
		}
		wait := backoffFor(results, pass)
		out.progress(map[string]any{"event": "retry", "pass": pass, "items": len(failedPaths), "wait_seconds": int(wait.Seconds())},
			fmt.Sprintf("retrying %d items in %s (pass %d)", len(failedPaths), wait, pass))
		time.Sleep(wait)
		paths, opts.Recursive = failedPaths, false
	}

	state.finish()
	out.summary(map[string]any{"event": "summary", "uploaded": totals[statusUploaded], "exists": totals[statusExists],
		"skipped": totals[statusSkipped], "failed": totals[statusFailed]},
		fmt.Sprintf("%d uploaded, %d already there, %d skipped, %d failed",
			totals[statusUploaded], totals[statusExists], totals[statusSkipped], totals[statusFailed]))
	if totals[statusFailed] > 0 {
		return errSomeFailed
	}
	return nil
}

var errInterrupted = fmt.Errorf("interrupted")

// uploadPass runs one upload of paths and returns every result. The upload
// itself is asynchronous inside core, so this waits for UploadStop.
func uploadPass(out *emitter, state *progressState, paths []string, opts core.UploadOptions, pass int) ([]core.FileUploadResult, bool) {
	rep := &collector{out: out, state: state, pass: pass, done: make(chan struct{}), started: time.Now()}
	mgr := core.NewUploadManager(rep, slog.New(slog.NewTextHandler(io.Discard, nil)))

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sig)
	go func() {
		select {
		case <-sig:
			rep.interrupt()
			mgr.Cancel()
		case <-rep.done:
		}
	}()

	mgr.Upload(paths, opts)
	<-rep.done
	rep.mu.Lock()
	defer rep.mu.Unlock()
	return rep.results, rep.interrupted
}

// classify maps a core result onto the four statuses ptg reports.
func classify(r core.FileUploadResult) (status, detail string) {
	switch {
	case r.IsError:
		return statusFailed, r.ErrorMessage
	case r.Skipped && r.SkipCode == "remote-duplicate":
		return statusExists, r.SkipReason
	case r.Skipped:
		return statusSkipped, r.SkipReason
	}
	return statusUploaded, r.MediaKey
}

// backoffFor grows the wait when Google is rate limiting or failing, which is
// the difference between a retry that works and one that adds load. A plain
// failure (one bad file) does not deserve minutes of waiting.
func backoffFor(results []core.FileUploadResult, pass int) time.Duration {
	var errs strings.Builder
	for _, r := range results {
		if r.IsError {
			errs.WriteString(strings.ToLower(r.ErrorMessage))
			errs.WriteByte(' ')
		}
	}
	s := errs.String()
	overloaded := strings.Contains(s, "429") || strings.Contains(s, "rate limit") ||
		strings.Contains(s, "resource_exhausted") || strings.Contains(s, "too many requests") ||
		strings.Contains(s, "status 500") || strings.Contains(s, "status 503")
	if !overloaded {
		return 5 * time.Second
	}
	wait := time.Duration(60<<(pass-1)) * time.Second
	if wait > 10*time.Minute {
		wait = 10 * time.Minute
	}
	return wait
}

// collector turns core's reporter callbacks into result lines and progress.
type collector struct {
	out     *emitter
	state   *progressState
	pass    int
	started time.Time

	mu          sync.Mutex
	results     []core.FileUploadResult
	total       int
	totalBytes  int64
	doneBytes   int64
	interrupted bool
	lastTick    time.Time
	closeOnce   sync.Once
	done        chan struct{}
}

func (c *collector) UploadStart(b core.UploadBatchStart) {
	c.mu.Lock()
	c.total, c.totalBytes = b.Total, b.TotalBytes
	c.mu.Unlock()
	c.state.setBatch(b.Total, b.TotalBytes)
}

func (c *collector) UploadStop() { c.closeOnce.Do(func() { close(c.done) }) }

func (c *collector) TotalBytes(n int64) {
	c.mu.Lock()
	c.totalBytes = n
	c.mu.Unlock()
	c.state.setBatch(c.total, n)
}

func (c *collector) TotalBytesDelta(d int64) {
	c.mu.Lock()
	c.totalBytes += d
	c.mu.Unlock()
	c.state.addBytes(d)
}

func (c *collector) Warning(w core.PreflightWarning) {
	c.out.progress(map[string]any{"event": "warning", "code": w.Code, "message": w.Message, "paths": w.Paths},
		"warning: "+w.Message)
}

// ThreadStatus arrives many times per file; it is throttled to one progress
// line every two seconds so a piped run is not drowned in it.
func (c *collector) ThreadStatus(t core.ThreadStatus) {
	c.state.setThread(t)
	c.mu.Lock()
	if time.Since(c.lastTick) < 2*time.Second {
		c.mu.Unlock()
		return
	}
	c.lastTick = time.Now()
	done, total, bytes, totalBytes := len(c.results), c.total, c.doneBytes+t.BytesUploaded, c.totalBytes
	c.mu.Unlock()
	c.out.progress(
		map[string]any{"event": "progress", "done": done, "total": total, "bytes": bytes,
			"total_bytes": totalBytes, "file": t.FileName, "state": t.Status},
		fmt.Sprintf("%d/%d items · %.1f/%.1f GB · %s %s",
			done, total, float64(bytes)/1e9, float64(totalBytes)/1e9, t.Status, t.FileName))
}

func (c *collector) FileResult(r core.FileUploadResult) {
	c.mu.Lock()
	c.results = append(c.results, r)
	c.mu.Unlock()

	status, detail := classify(r)
	c.state.addResult(resultRecord{Status: status, Path: r.Path, Detail: detail, Paths: r.Paths,
		Pass: c.pass, At: time.Now().Unix()})
	rec := map[string]any{"event": "result", "status": status, "path": r.Path, "pass": c.pass,
		"live_photo": r.IsLivePhoto}
	line := []string{status, r.Path, detail}
	if len(r.Paths) > 1 {
		rec["paths"] = r.Paths
		line = append(line, "paired="+r.Paths[1])
	}
	switch status {
	case statusUploaded:
		rec["media_key"] = r.MediaKey
	case statusFailed:
		rec["error"] = r.ErrorMessage
	default:
		rec["skip_code"], rec["reason"] = r.SkipCode, r.SkipReason
	}
	_ = c.out.record(rec, tabs(line...))
}

func (c *collector) AlbumProgress(core.AlbumStatus) {}

func (c *collector) AlbumComplete(a core.AlbumStatus) {
	c.out.progress(map[string]any{"event": "album", "album": a.AlbumName, "added": a.ItemsAdded, "total": a.TotalItems},
		fmt.Sprintf("album %q: %d of %d items added", a.AlbumName, a.ItemsAdded, a.TotalItems))
}

func (c *collector) AlbumError(a core.AlbumError) {
	c.out.progress(map[string]any{"event": "album_error", "album": a.AlbumName, "error": a.Error},
		"album error: "+a.Error)
}

func (c *collector) interrupt() {
	c.mu.Lock()
	c.interrupted = true
	c.mu.Unlock()
}

func isTerminal(f *os.File) bool {
	fi, err := f.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}
