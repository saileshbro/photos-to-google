package cli

import (
	"strings"
	"testing"

	"github.com/saileshbro/photos-to-google/internal/gotohp/core"
)

func TestResolvePathsReadsArgsAndStdin(t *testing.T) {
	got, err := resolvePaths(strings.NewReader("one.jpg\n\ntwo.jpg\n"), []string{"arg.heic", "-"}, false)
	if err != nil {
		t.Fatalf("resolvePaths: %v", err)
	}
	want := []string{"arg.heic", "one.jpg", "two.jpg"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestResolvePathsNullSeparated(t *testing.T) {
	got, err := resolvePaths(strings.NewReader("a b.jpg\x00c.mov\x00"), []string{"-"}, true)
	if err != nil {
		t.Fatalf("resolvePaths: %v", err)
	}
	if len(got) != 2 || got[0] != "a b.jpg" {
		t.Errorf("got %v, want [a b.jpg c.mov]", got)
	}
}

func TestResolvePathsEmptyIsUsageError(t *testing.T) {
	_, err := resolvePaths(strings.NewReader(""), []string{"-"}, false)
	var usage UsageError
	if err == nil || !asUsage(err, &usage) {
		t.Fatalf("want a usage error, got %v", err)
	}
}

func TestClassify(t *testing.T) {
	for _, tc := range []struct {
		name   string
		in     core.FileUploadResult
		status string
	}{
		{"uploaded", core.FileUploadResult{MediaKey: "AF1Qip"}, statusUploaded},
		{"exists", core.FileUploadResult{Skipped: true, SkipCode: "remote-duplicate"}, statusExists},
		{"live component exists", core.FileUploadResult{Skipped: true, SkipCode: "remote-live-photo-component-exists"}, statusSkipped},
		{"failed", core.FileUploadResult{IsError: true, ErrorMessage: "status 503"}, statusFailed},
	} {
		if got, _ := classify(tc.in); got != tc.status {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.status)
		}
	}
}

// backoffFor must tell an overloaded Google apart from one bad file: waiting
// minutes on a single rejected file wastes a run, and hammering through 503s
// makes them worse.
func TestBackoffForDistinguishesOverload(t *testing.T) {
	oneBadFile := []core.FileUploadResult{{IsError: true, ErrorMessage: "unsupported format"}}
	if got := backoffFor(oneBadFile, 1); got.Seconds() != 5 {
		t.Errorf("single failure: got %s, want 5s", got)
	}
	overload := []core.FileUploadResult{{IsError: true, ErrorMessage: "request failed with status 503"}}
	if got := backoffFor(overload, 1); got.Seconds() != 60 {
		t.Errorf("first overload: got %s, want 60s", got)
	}
	if got := backoffFor(overload, 4); got != 480_000_000_000 {
		t.Errorf("fourth overload: got %s, want 8m", got)
	}
	if got := backoffFor(overload, 9); got.Minutes() != 10 {
		t.Errorf("capped: got %s, want 10m", got)
	}
}
