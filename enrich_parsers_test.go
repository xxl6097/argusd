package argus

import (
	"context"
	"os/exec"
	"testing"
)

func TestLoadARPCommandEmptyArgv(t *testing.T) {
	// Empty argv is a no-op.
	hints := map[string]Hint{}
	loadARPCommand(context.Background(), nil, hints)
	if len(hints) != 0 {
		t.Errorf("empty argv should not mutate hints, got %+v", hints)
	}
}

func TestLoadARPCommandBadExecutable(t *testing.T) {
	// Non-existent binary is silently dropped (no panic).
	hints := map[string]Hint{}
	loadARPCommand(context.Background(), []string{"/no/such/binary"}, hints)
	if len(hints) != 0 {
		t.Errorf("bad exec should leave hints empty, got %+v", hints)
	}
}

func TestLoadARPCommandParsesEchoOutput(t *testing.T) {
	// Use /bin/echo (universally present) to emit a synthetic `ip neigh show`
	// payload, verify the parser handles a realistic line shape: IPv6 rows
	// skipped, FAILED/INCOMPLETE skipped, IPv4 + lladdr + REACHABLE kept.
	if _, err := exec.LookPath("echo"); err != nil {
		t.Skip("echo not in PATH")
	}
	payload := "192.168.1.10 dev br-lan lladdr AA:BB:CC:DD:EE:01 REACHABLE\n" +
		"192.168.1.11 dev br-lan lladdr aa:bb:cc:dd:ee:02 STALE\n" +
		"192.168.1.12 dev br-lan FAILED\n" +
		"fe80::1 dev br-lan lladdr aa:bb:cc:dd:ee:ff STALE\n"

	hints := map[string]Hint{}
	loadARPCommand(context.Background(), []string{"echo", payload}, hints)

	// 2 IPv4 entries kept (REACHABLE + STALE); FAILED and IPv6 dropped.
	if len(hints) != 2 {
		t.Errorf("want 2 entries kept, got %d: %+v", len(hints), hints)
	}
	if got := hints["aa:bb:cc:dd:ee:01"]; got.IP != "192.168.1.10" {
		t.Errorf("REACHABLE row: %+v", got)
	}
	if got := hints["aa:bb:cc:dd:ee:02"]; got.IP != "192.168.1.11" {
		t.Errorf("STALE row (lowercased): %+v", got)
	}
}
