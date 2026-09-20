package cli

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestServeProgressServesPageAndState(t *testing.T) {
	state := newProgressState()
	state.setBatch(2, 1000)
	state.addResult(resultRecord{Status: statusUploaded, Path: "/tmp/a.heic", Detail: "AF1Qip"})
	state.addResult(resultRecord{Status: statusFailed, Path: "/tmp/b.mov", Detail: "status 503"})

	url, err := serveProgress("127.0.0.1:0", state)
	if err != nil {
		t.Fatalf("serveProgress: %v", err)
	}
	// serveProgress reports a LAN address for an unspecified host; this test
	// binds a loopback one, so the URL is directly usable.
	body := get(t, url+"/progress.json")
	var snap map[string]any
	if err := json.Unmarshal([]byte(body), &snap); err != nil {
		t.Fatalf("progress.json is not JSON: %v", err)
	}
	if snap["done"].(float64) != 2 {
		t.Errorf("done = %v, want 2", snap["done"])
	}
	counts := snap["counts"].(map[string]any)
	if counts[statusFailed].(float64) != 1 {
		t.Errorf("failed count = %v, want 1", counts[statusFailed])
	}
	if len(snap["failures"].([]any)) != 1 {
		t.Errorf("want the failure listed for the page to show")
	}

	if page := get(t, url+"/"); !strings.Contains(page, "progress.json") {
		t.Errorf("the page does not poll progress.json")
	}
}

func get(t *testing.T, url string) string {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s: %s", url, resp.Status)
	}
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
