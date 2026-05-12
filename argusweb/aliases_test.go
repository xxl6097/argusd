package argusweb_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	argus "github.com/xxl6097/argusd"
	"github.com/xxl6097/argusd/argustest"
	"github.com/xxl6097/argusd/argusweb"
)

// --- AliasStore unit tests ------------------------------------------------

func TestAliasStoreInMemorySetAndLookup(t *testing.T) {
	s := argusweb.NewAliasStore("")
	if got := s.Lookup("aa:bb:cc:dd:ee:ff"); got != "" {
		t.Errorf("empty store returned %q, want empty", got)
	}
	if err := s.Set("aa:bb:cc:dd:ee:ff", "Alice"); err != nil {
		t.Fatal(err)
	}
	if got := s.Lookup("AA:BB:CC:DD:EE:FF"); got != "Alice" {
		t.Errorf("case-insensitive lookup failed: %q", got)
	}
}

func TestAliasStoreEmptyNameDeletes(t *testing.T) {
	s := argusweb.NewAliasStore("")
	_ = s.Set("aa", "x")
	if err := s.Set("aa", ""); err != nil {
		t.Fatal(err)
	}
	if got := s.Lookup("aa"); got != "" {
		t.Errorf("after empty Set, got %q want empty", got)
	}
}

func TestAliasStorePersistsAcrossInstances(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "aliases.json")

	s1 := argusweb.NewAliasStore(path)
	if err := s1.Set("aa:bb:cc:dd:ee:ff", "Bob"); err != nil {
		t.Fatal(err)
	}

	// Second instance reads the same file.
	s2 := argusweb.NewAliasStore(path)
	if got := s2.Lookup("aa:bb:cc:dd:ee:ff"); got != "Bob" {
		t.Errorf("after reopen: got %q want Bob", got)
	}
}

func TestAliasStoreCorruptFileBecomesEmpty(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "aliases.json")
	// write garbage
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := argusweb.NewAliasStore(path)
	if got := s.Lookup("anything"); got != "" {
		t.Errorf("corrupt file should load as empty; got %q", got)
	}
	// First successful Set repairs the file.
	if err := s.Set("aa", "X"); err != nil {
		t.Fatal(err)
	}
	s2 := argusweb.NewAliasStore(path)
	if got := s2.Lookup("aa"); got != "X" {
		t.Errorf("after repair-on-Set: got %q want X", got)
	}
}

func TestAliasStoreNameLengthLimit(t *testing.T) {
	s := argusweb.NewAliasStore("")
	long := strings.Repeat("a", 65)
	if err := s.Set("aa", long); err == nil {
		t.Error("expected error for >64 name, got nil")
	}
	if err := s.Set("", "x"); err == nil {
		t.Error("expected error for empty MAC, got nil")
	}
}

// --- /api/devices merge test ----------------------------------------------

func TestDevicesRespondsWithAliasField(t *testing.T) {
	w := argus.New(argus.WithFetcher(&argustest.FixedFetcher{}))
	store := argusweb.NewAliasStore("")
	_ = store.Set("aa:bb:cc:dd:ee:ff", "My Phone")

	srv := argusweb.NewServer(w, argusweb.WithAliases(store))
	ts := httptest.NewServer(srv)
	defer ts.Close()

	srv.OnEvent(argus.Event{
		Time:   time.Now(),
		Kind:   argus.EventOffline,
		Device: argus.Device{MAC: "aa:bb:cc:dd:ee:ff", IP: "10.0.0.9", Hostname: "mac-only"},
	})

	body := fetchDevicesBody(t, ts)
	if len(body.Devices) != 1 {
		t.Fatalf("expected 1 device, got %+v", body.Devices)
	}
	if got := body.Devices[0]["alias"]; got != "My Phone" {
		t.Errorf("alias = %v, want 'My Phone'", got)
	}
	if got := body.Devices[0]["hostname"]; got != "mac-only" {
		t.Errorf("hostname should remain unchanged (%v)", got)
	}
}

// --- REST endpoint tests --------------------------------------------------

func TestAliasesGetReturnsStored(t *testing.T) {
	w := argus.New(argus.WithFetcher(&argustest.FixedFetcher{}))
	store := argusweb.NewAliasStore("")
	_ = store.Set("aa:bb:cc:dd:ee:ff", "Alice")

	srv := argusweb.NewServer(w, argusweb.WithAliases(store))
	ts := httptest.NewServer(srv)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/aliases")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body struct {
		Aliases map[string]string `json:"aliases"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Aliases["AA:BB:CC:DD:EE:FF"] != "Alice" {
		t.Errorf("aliases = %+v", body.Aliases)
	}
}

func TestAliasesPostWritesStore(t *testing.T) {
	w := argus.New(argus.WithFetcher(&argustest.FixedFetcher{}))
	store := argusweb.NewAliasStore("")

	srv := argusweb.NewServer(w,
		argusweb.WithAliases(store),
		// httptest uses 127.0.0.1, so default LAN auth allows it; be explicit.
		argusweb.WithWriteAuth(func(r *http.Request) bool { return true }),
	)
	ts := httptest.NewServer(srv)
	defer ts.Close()

	req, _ := http.NewRequest("POST", ts.URL+"/api/aliases",
		bytes.NewReader([]byte(`{"mac":"aa:bb:cc:dd:ee:ff","name":"Carol"}`)))
	req.Header.Set("Content-Type", "application/json")
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if got := store.Lookup("aa:bb:cc:dd:ee:ff"); got != "Carol" {
		t.Errorf("store not updated: %q", got)
	}
}

func TestAliasesPostEmptyNameDeletes(t *testing.T) {
	w := argus.New(argus.WithFetcher(&argustest.FixedFetcher{}))
	store := argusweb.NewAliasStore("")
	_ = store.Set("aa", "x")

	srv := argusweb.NewServer(w,
		argusweb.WithAliases(store),
		argusweb.WithWriteAuth(func(r *http.Request) bool { return true }),
	)
	ts := httptest.NewServer(srv)
	defer ts.Close()

	req, _ := http.NewRequest("POST", ts.URL+"/api/aliases",
		bytes.NewReader([]byte(`{"mac":"aa","name":""}`)))
	req.Header.Set("Content-Type", "application/json")
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if got := store.Lookup("aa"); got != "" {
		t.Errorf("empty-name POST should delete; still got %q", got)
	}
}

func TestAliasesDeleteRemoves(t *testing.T) {
	w := argus.New(argus.WithFetcher(&argustest.FixedFetcher{}))
	store := argusweb.NewAliasStore("")
	_ = store.Set("aa:bb:cc:dd:ee:ff", "Dave")

	srv := argusweb.NewServer(w,
		argusweb.WithAliases(store),
		argusweb.WithWriteAuth(func(r *http.Request) bool { return true }),
	)
	ts := httptest.NewServer(srv)
	defer ts.Close()

	req, _ := http.NewRequest("DELETE", ts.URL+"/api/aliases?mac=AA:BB:CC:DD:EE:FF", nil)
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if got := store.Lookup("aa:bb:cc:dd:ee:ff"); got != "" {
		t.Errorf("after DELETE got %q want empty", got)
	}
}

func TestAliasesWithoutStoreReturns503(t *testing.T) {
	w := argus.New(argus.WithFetcher(&argustest.FixedFetcher{}))
	srv := argusweb.NewServer(w) // no WithAliases
	ts := httptest.NewServer(srv)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/aliases")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 503 {
		t.Errorf("status = %d, want 503", resp.StatusCode)
	}
}

func TestAliasesWriteRejectedByDefaultAuth(t *testing.T) {
	// Override default auth with one that rejects everything, simulating
	// a remote caller that is not on the LAN.
	w := argus.New(argus.WithFetcher(&argustest.FixedFetcher{}))
	store := argusweb.NewAliasStore("")

	srv := argusweb.NewServer(w,
		argusweb.WithAliases(store),
		argusweb.WithWriteAuth(func(r *http.Request) bool { return false }),
	)
	ts := httptest.NewServer(srv)
	defer ts.Close()

	req, _ := http.NewRequest("POST", ts.URL+"/api/aliases",
		bytes.NewReader([]byte(`{"mac":"aa","name":"x"}`)))
	req.Header.Set("Content-Type", "application/json")
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 403 {
		t.Errorf("status = %d, want 403", resp.StatusCode)
	}
	// Read path is NOT gated — should still succeed.
	r2, err := http.Get(ts.URL + "/api/aliases")
	if err != nil {
		t.Fatal(err)
	}
	defer r2.Body.Close()
	if r2.StatusCode != 200 {
		t.Errorf("GET status = %d, want 200 (reads are not auth-gated)", r2.StatusCode)
	}
}

func TestAliasesPostRejectsBadJSON(t *testing.T) {
	w := argus.New(argus.WithFetcher(&argustest.FixedFetcher{}))
	store := argusweb.NewAliasStore("")
	srv := argusweb.NewServer(w,
		argusweb.WithAliases(store),
		argusweb.WithWriteAuth(func(r *http.Request) bool { return true }),
	)
	ts := httptest.NewServer(srv)
	defer ts.Close()

	req, _ := http.NewRequest("POST", ts.URL+"/api/aliases", bytes.NewReader([]byte("not-json")))
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

// Suppress unused-import warning from some matrix runs.
var _ = context.Background
