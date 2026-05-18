//go:build linux

package argus

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// linuxLogreadAttrs returns SysProcAttr that ensures the spawned
// `logread -f` child:
//
//   - Receives SIGTERM the instant the parent dies (Pdeathsig). This is
//     the Linux kernel "prctl(PR_SET_PDEATHSIG)" mechanism — it fires
//     even when the parent is SIGKILL'd / OOM-killed / segfaults, where
//     Go-side defer cleanup never runs.
//   - Lives in its own process group (Setpgid). On hard cleanup we can
//     kill the whole group with one syscall, and stray descendants
//     (e.g. logread itself spawned by busybox via /bin/sh) are caught.
//
// On platforms other than Linux the function in logwatch_other.go
// returns nil, so the regular non-Linux paths still compile — argusd
// itself is Linux-only in production but we keep go test ./... usable
// from macOS / Windows dev hosts.
func linuxLogreadAttrs() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{
		Pdeathsig: syscall.SIGTERM,
		Setpgid:   true,
	}
}

// reapOrphanedLogreads scans /proc for logread processes that have been
// reparented to init (PPid == 1) and whose cmdline contains "logread"
// with a "-f" flag — these are stale streamers from a prior argusd
// run that crashed without cleaning them up.
//
// Best-effort: SIGTERM each match, ignore errors (we may not be allowed
// to kill some — non-fatal). Returns the number of processes signaled.
//
// Safety: only matches "logread" with "-f" in argv, so we won't touch
// vendor logread daemons running under procd init scripts (they have
// PPid == 1 too but typically take a config file or different flags;
// vendor C-Life uses "/sbin/logread -f -F /data/log/system_log -p ..."
// which would also match, so we additionally require the cmdline to be
// EXACTLY "logread -f" or "/usr/sbin/logread -f" — the form Go's
// exec.Command spawns).
func reapOrphanedLogreads() int {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return 0
	}
	myPid := os.Getpid()
	reaped := 0
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		pid, err := strconv.Atoi(e.Name())
		if err != nil || pid == myPid || pid == 1 {
			continue
		}
		// Read PPid from /proc/<pid>/status
		statusBytes, err := os.ReadFile(filepath.Join("/proc", e.Name(), "status"))
		if err != nil {
			continue
		}
		ppid := 0
		for _, line := range strings.Split(string(statusBytes), "\n") {
			if strings.HasPrefix(line, "PPid:") {
				ppid, _ = strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(line, "PPid:")))
				break
			}
		}
		if ppid != 1 {
			continue // not orphaned
		}
		cmdlineBytes, err := os.ReadFile(filepath.Join("/proc", e.Name(), "cmdline"))
		if err != nil {
			continue
		}
		// /proc/<pid>/cmdline uses NULs as separators; turn into space
		// for matching.
		cmdline := strings.TrimRight(strings.ReplaceAll(string(cmdlineBytes), "\x00", " "), " ")
		// Strict match: must be exactly "logread -f" or
		// "/usr/sbin/logread -f" / "/sbin/logread -f". Reject anything
		// with extra args (vendor logread daemons take -F/-S/-p flags).
		if cmdline != "logread -f" &&
			cmdline != "/usr/sbin/logread -f" &&
			cmdline != "/sbin/logread -f" {
			continue
		}
		if err := syscall.Kill(pid, syscall.SIGTERM); err == nil {
			reaped++
		}
	}
	return reaped
}
