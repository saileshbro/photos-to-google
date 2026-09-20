package cli

import (
	"embed"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/saileshbro/photos-to-google/internal/gotohp/core"
)

//go:embed web/progress.html
var webFiles embed.FS

// progressState is what a run knows about itself. The upload writes it, the
// progress page reads it; a run without --serve keeps it anyway, because the
// summary comes from here too.
type progressState struct {
	mu sync.Mutex

	started  time.Time
	pass     int
	total    int
	bytes    int64
	doneByte int64
	counts   map[string]int
	current  map[int]core.ThreadStatus
	recent   []resultRecord // newest last, capped
	failures []resultRecord
	finished bool
}

type resultRecord struct {
	Status string   `json:"status"`
	Path   string   `json:"path"`
	Detail string   `json:"detail"`
	Paths  []string `json:"paths,omitempty"`
	Pass   int      `json:"pass"`
	At     int64    `json:"at"`
}

const recentCap = 200

func newProgressState() *progressState {
	return &progressState{started: time.Now(), counts: map[string]int{}, current: map[int]core.ThreadStatus{}}
}

func (s *progressState) addResult(r resultRecord) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.counts[r.Status]++
	s.recent = append(s.recent, r)
	if len(s.recent) > recentCap {
		s.recent = s.recent[len(s.recent)-recentCap:]
	}
	if r.Status == statusFailed {
		s.failures = append(s.failures, r)
	}
}

func (s *progressState) setBatch(total int, bytes int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.total, s.bytes = total, bytes
}

func (s *progressState) addBytes(delta int64) {
	s.mu.Lock()
	s.bytes += delta
	s.mu.Unlock()
}

func (s *progressState) setThread(t core.ThreadStatus) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.current[t.WorkerID] = t
	if t.Status == "completed" {
		s.doneByte += t.BytesTotal
	}
}

func (s *progressState) setPass(p int) {
	s.mu.Lock()
	s.pass = p
	s.mu.Unlock()
}

func (s *progressState) finish() {
	s.mu.Lock()
	s.finished = true
	s.current = map[int]core.ThreadStatus{}
	s.mu.Unlock()
}

func (s *progressState) snapshot() map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	threads := make([]core.ThreadStatus, 0, len(s.current))
	for _, t := range s.current {
		if t.Status != "idle" && t.FileName != "" {
			threads = append(threads, t)
		}
	}
	done := 0
	for _, n := range s.counts {
		done += n
	}
	return map[string]any{
		"v":           SchemaVersion,
		"now":         time.Now().Unix(),
		"started":     s.started.Unix(),
		"elapsed":     int(time.Since(s.started).Seconds()),
		"pass":        s.pass,
		"total":       s.total,
		"done":        done,
		"counts":      s.counts,
		"bytes_total": s.bytes,
		"bytes_done":  s.doneByte,
		"threads":     threads,
		"recent":      s.recent,
		"failures":    s.failures,
		"finished":    s.finished,
	}
}

// serveProgress starts the progress page. It listens on addr and returns the
// URL it is reachable at. The page is read-only: there is nothing to press, so
// leaving it open on a phone cannot disturb a run.
func serveProgress(addr string, state *progressState) (string, error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return "", usageErrorf("cannot serve the progress page on %s: %v", addr, err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/progress.json", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(state.snapshot())
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		page, _ := webFiles.ReadFile("web/progress.html")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(page)
	})
	go func() { _ = http.Serve(ln, mux) }()

	host := ln.Addr().String()
	if tcp, ok := ln.Addr().(*net.TCPAddr); ok && tcp.IP.IsUnspecified() {
		host = fmt.Sprintf("%s:%d", lanAddress(), tcp.Port)
	}
	return "http://" + host, nil
}

// lanAddress reports an address other machines on this network can reach, so
// the URL can be opened on a phone rather than only on this Mac.
func lanAddress() string {
	conn, err := net.Dial("udp", "8.8.8.8:80") // no packet is sent; this picks the route
	if err != nil {
		name, _ := os.Hostname()
		return name
	}
	defer conn.Close()
	return conn.LocalAddr().(*net.UDPAddr).IP.String()
}
