//go:build !linux

package argus

import "syscall"

// linuxLogreadAttrs is a no-op on non-Linux hosts. argusd targets
// Linux for production, but unit tests run on macOS / Windows dev
// hosts; returning nil keeps the build green there. The kernel-level
// Pdeathsig protection only exists on Linux.
func linuxLogreadAttrs() *syscall.SysProcAttr { return nil }

// reapOrphanedLogreads is a no-op on non-Linux hosts (no /proc to scan,
// and orphan reaping is a Linux-deployment concern only). Returns 0.
func reapOrphanedLogreads() int { return 0 }
