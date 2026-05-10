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

// --- offline cache + /api/devices status surface (v0.13.3) --------------

// fetchDevicesBody is a test helper that GETs /api/devices against the
// given server and decodes the JSON body into a typed struct.
func fetchDevicesBody(t *testing.T, ts *httptest.Server) devicesBody {
	t.Helper()
	resp, err := http.Get(ts.URL + "/api/devices")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body devicesBody
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	return body
}

type devicesBody struct {
	Count   int              `json:"count"`
	Online  int              `json:"online"`
	Offline int              `json:"offline"`
	Devices []map[string]any `json:"devices"`
}

func TestDevicesOfflineEventRetainsDevice(t *testing.T) {
	// After an Offline event, the device should still appear in
	// /api/devices with status="offline".
	w := argus.New(argus.WithFetcher(&argustest.FixedFetcher{}))
	srv := argusweb.NewServer(w)
	ts := httptest.NewServer(srv)
	defer ts.Close()

	srv.OnEvent(argus.Event{
		Time:   time.Now(),
		Kind:   argus.EventOffline,
		Device: argus.Device{MAC: "aa:bb:cc:dd:ee:ff", IP: "10.0.0.9", Hostname: "phone1", Radio: "5G"},
	})

	body := fetchDevicesBody(t, ts)
	if body.Count != 1 || body.Offline != 1 || body.Online != 0 {
		t.Fatalf("count=%d online=%d offline=%d, want 1/0/1 (%+v)", body.Count, body.Online, body.Offline, body.Devices)
	}
	d := body.Devices[0]
	if d["status"] != "offline" {
		t.Errorf("status = %v, want offline", d["status"])
	}
	if d["mac"] != "AA:BB:CC:DD:EE:FF" {
		t.Errorf("mac = %v, want AA:BB:CC:DD:EE:FF", d["mac"])
	}
	if _, ok := d["offline_at_ms"]; !ok {
		t.Errorf("offline_at_ms missing: %+v", d)
	}
}

func TestDevicesOnlineEventEvictsFromOffline(t *testing.T) {
	// Offline -> Online: device should move back to status="online".
	w := argus.New(argus.WithFetcher(&argustest.FixedFetcher{}))
	srv := argusweb.NewServer(w)
	ts := httptest.NewServer(srv)
	defer ts.Close()

	dev := argus.Device{MAC: "aa:bb:cc:dd:ee:ff", IP: "10.0.0.9", Radio: "5G"}
	srv.OnEvent(argus.Event{Time: time.Now(), Kind: argus.EventOffline, Device: dev})

	before := fetchDevicesBody(t, ts)
	if before.Offline != 1 {
		t.Fatalf("before: offline=%d want 1", before.Offline)
	}

	// Online fires; our watcher's Known() still returns empty (no Fetcher
	// data), but the offline cache should be evicted immediately.
	srv.OnEvent(argus.Event{Time: time.Now(), Kind: argus.EventOnline, Device: dev})

	after := fetchDevicesBody(t, ts)
	if after.Offline != 0 {
		t.Errorf("after: offline=%d want 0 (%+v)", after.Offline, after.Devices)
	}
}

func TestDevicesOfflineRetentionTTL(t *testing.T) {
	// With a very short TTL, an old offline entry should be dropped
	// on the next /api/devices request.
	w := argus.New(argus.WithFetcher(&argustest.FixedFetcher{}))
	srv := argusweb.NewServer(w, argusweb.WithOfflineRetention(20*time.Millisecond))
	ts := httptest.NewServer(srv)
	defer ts.Close()

	srv.OnEvent(argus.Event{
		Time:   time.Now(),
		Kind:   argus.EventOffline,
		Device: argus.Device{MAC: "aa:bb:cc:dd:ee:ff", IP: "10.0.0.9"},
	})

	if got := fetchDevicesBody(t, ts); got.Offline != 1 {
		t.Fatalf("pre-TTL: offline=%d want 1", got.Offline)
	}

	time.Sleep(50 * time.Millisecond)

	if got := fetchDevicesBody(t, ts); got.Offline != 0 {
		t.Errorf("post-TTL: offline=%d want 0 (%+v)", got.Offline, got.Devices)
	}
}

func TestDevicesOfflineCapEvictsOldest(t *testing.T) {
	// With max=2, firing 3 Offline events should keep only the 2 newest.
	w := argus.New(argus.WithFetcher(&argustest.FixedFetcher{}))
	srv := argusweb.NewServer(w, argusweb.WithOfflineMax(2))
	ts := httptest.NewServer(srv)
	defer ts.Close()

	// Use offset timestamps within the TTL window so ordering is
	// deterministic while still being "recent enough" to survive prune.
	base := time.Now().Add(-3 * time.Second)
	srv.OnEvent(argus.Event{Time: base.Add(-2 * time.Second), Kind: argus.EventOffline, Device: argus.Device{MAC: "aa:aa:aa:aa:aa:01"}})
	srv.OnEvent(argus.Event{Time: base.Add(-1 * time.Second), Kind: argus.EventOffline, Device: argus.Device{MAC: "aa:aa:aa:aa:aa:02"}})
	srv.OnEvent(argus.Event{Time: base, Kind: argus.EventOffline, Device: argus.Device{MAC: "aa:aa:aa:aa:aa:03"}})

	body := fetchDevicesBody(t, ts)
	if body.Offline != 2 {
		t.Fatalf("offline=%d want 2 (%+v)", body.Offline, body.Devices)
	}
	for _, d := range body.Devices {
		if d["mac"] == "AA:AA:AA:AA:AA:01" {
			t.Errorf("oldest entry 01 should have been evicted; got %+v", body.Devices)
		}
	}
}

func TestDevicesChangeEventUpdatesOfflineCacheEntry(t *testing.T) {
	// A Change event for a device currently in the offline cache should
	// refresh its payload (e.g. DHCP renewed IP) without changing status.
	w := argus.New(argus.WithFetcher(&argustest.FixedFetcher{}))
	srv := argusweb.NewServer(w)
	ts := httptest.NewServer(srv)
	defer ts.Close()

	srv.OnEvent(argus.Event{
		Time:   time.Now(),
		Kind:   argus.EventOffline,
		Device: argus.Device{MAC: "aa:bb:cc:dd:ee:ff", IP: "10.0.0.9"},
	})
	srv.OnEvent(argus.Event{
		Time:   time.Now(),
		Kind:   argus.EventChange,
		Device: argus.Device{MAC: "aa:bb:cc:dd:ee:ff", IP: "10.0.0.42"},
	})

	body := fetchDevicesBody(t, ts)
	if body.Offline != 1 || len(body.Devices) != 1 {
		t.Fatalf("offline=%d count=%d (%+v)", body.Offline, len(body.Devices), body.Devices)
	}
	if body.Devices[0]["ip"] != "10.0.0.42" {
		t.Errorf("ip = %v, want 10.0.0.42 (Change didn't refresh offline entry)", body.Devices[0]["ip"])
	}
	if body.Devices[0]["status"] != "offline" {
		t.Errorf("status = %v, want offline", body.Devices[0]["status"])
	}
}

func TestDevicesStatusFieldAlwaysPresent(t *testing.T) {
	// Every row must carry a status field, even when the offline cache
	// is empty. Relied on by the dashboard for row styling.
	w := argus.New(argus.WithFetcher(&argustest.FixedFetcher{}))
	srv := argusweb.NewServer(w)
	ts := httptest.NewServer(srv)
	defer ts.Close()

	srv.OnEvent(argus.Event{Time: time.Now(), Kind: argus.EventOffline, Device: argus.Device{MAC: "aa:bb:cc:dd:ee:ff"}})

	body := fetchDevicesBody(t, ts)
	for _, d := range body.Devices {
		if _, ok := d["status"]; !ok {
			t.Errorf("status missing on row %+v", d)
		}
	}
}
