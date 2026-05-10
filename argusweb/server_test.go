package argusweb_test

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	argus "github.com/xxl6097/argus"
	"github.com/xxl6097/argus/argustest"
	"github.com/xxl6097/argus/argusweb"
)

func TestIndexServesHTML(t *testing.T) {
	w := argus.New(argus.WithFetcher(&argustest.FixedFetcher{}))
	srv := argusweb.NewServer(w)
	ts := httptest.NewServer(srv)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("status = %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("content-type = %q", ct)
	}
	body, _ := io.ReadAll(resp.Body)
	for _, want := range []string{"<!DOCTYPE html>", "Argus", "/api/events", "/api/devices"} {
		if !strings.Contains(string(body), want) {
			t.Errorf("body missing %q", want)
		}
	}
}

func TestIndexNotFound(t *testing.T) {
	w := argus.New(argus.WithFetcher(&argustest.FixedFetcher{}))
	srv := argusweb.NewServer(w)
	ts := httptest.NewServer(srv)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/does-not-exist")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}

func TestDevicesEndpointReturnsEmpty(t *testing.T) {
	w := argus.New(argus.WithFetcher(&argustest.FixedFetcher{}))
	srv := argusweb.NewServer(w)
	ts := httptest.NewServer(srv)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/devices")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	var body struct {
		Count   int              `json:"count"`
		Devices []map[string]any `json:"devices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	// Watcher has never run; Known() returns empty map.
	if body.Count != 0 {
		t.Errorf("count = %d, want 0", body.Count)
	}
}

func TestSSEReceivesEvent(t *testing.T) {
	w := argus.New(argus.WithFetcher(&argustest.FixedFetcher{}))
	srv := argusweb.NewServer(w)
	ts := httptest.NewServer(srv)
	defer ts.Close()

	// Connect the SSE client.
	req, _ := http.NewRequest("GET", ts.URL+"/api/events", nil)
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Errorf("content-type = %q", ct)
	}

	// Reader for the stream.
	br := bufio.NewReader(resp.Body)

	// First frame must be the hello event.
	var got strings.Builder
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		line, err := br.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		got.WriteString(line)
		if strings.Contains(got.String(), "event: hello") &&
			strings.Contains(got.String(), `"ok":true`) {
			break
		}
	}
	if !strings.Contains(got.String(), "event: hello") {
		t.Fatalf("did not receive hello: %q", got.String())
	}

	// Fire an event from another goroutine — should show up in the stream.
	var done sync.WaitGroup
	done.Add(1)
	go func() {
		defer done.Done()
		time.Sleep(30 * time.Millisecond)
		srv.OnEvent(argus.Event{
			Time:   time.Now(),
			Kind:   argus.EventOnline,
			Device: argus.Device{MAC: "aa:bb:cc:dd:ee:ff", IP: "10.0.0.1"},
		})
	}()

	// Read until we see ONLINE *and* a data: line with the MAC, or 2s timeout.
	// An SSE frame is `event: KIND\ndata: {...}\n\n`; simply breaking on the
	// `event:` line loses the `data:` payload.
	saw := ""
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		line, err := br.ReadString('\n')
		if err != nil {
			break
		}
		saw += line
		if strings.Contains(saw, "event: ONLINE") &&
			strings.Contains(strings.ToLower(saw), "aa:bb:cc:dd:ee:ff") {
			break
		}
	}
	done.Wait()
	if !strings.Contains(saw, "event: ONLINE") {
		t.Fatalf("SSE did not receive ONLINE event:\n%s", saw)
	}
	if !strings.Contains(strings.ToLower(saw), "aa:bb:cc:dd:ee:ff") {
		t.Errorf("SSE payload missing MAC; body:\n%s", saw)
	}
}

func TestSSESlowSubscriberDoesNotBlock(t *testing.T) {
	// A subscriber that never reads must not wedge other fanout. We can't
	// easily simulate a real stuck HTTP client in httptest, so this
	// exercises the internal OnEvent drop path: fire more than the 8-slot
	// buffer; OnEvent should return promptly each time without blocking.
	w := argus.New(argus.WithFetcher(&argustest.FixedFetcher{}))
	srv := argusweb.NewServer(w)

	// Spin up a subscriber that reads slowly.
	ts := httptest.NewServer(srv)
	defer ts.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	req, _ := http.NewRequestWithContext(ctx, "GET", ts.URL+"/api/events", nil)
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	// Don't read from resp.Body — the subscriber becomes slow.
	// Fire 100 events; OnEvent must not block.
	start := time.Now()
	for i := 0; i < 100; i++ {
		srv.OnEvent(argus.Event{Kind: argus.EventOnline})
	}
	elapsed := time.Since(start)
	if elapsed > 500*time.Millisecond {
		t.Errorf("OnEvent was slow: 100 events took %v — slow-subscriber should drop", elapsed)
	}
}

func TestShutdownClosesSubscribers(t *testing.T) {
	w := argus.New(argus.WithFetcher(&argustest.FixedFetcher{}))
	srv := argusweb.NewServer(w)
	ts := httptest.NewServer(srv)
	defer ts.Close()

	req, _ := http.NewRequest("GET", ts.URL+"/api/events", nil)
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	// Give the subscription time to register.
	time.Sleep(50 * time.Millisecond)

	srv.Shutdown(context.Background())
	// After Shutdown, new events simply have nowhere to go — smoke test
	// that nothing panics.
	srv.OnEvent(argus.Event{Kind: argus.EventOnline})
}
