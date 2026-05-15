package argus

import "strings"

// LookupVendor returns a human-readable vendor name for the OUI portion
// (first 3 octets) of a MAC address, or "" when the OUI is not in the
// built-in table. The lookup is best-effort: argus ships a curated subset
// of the IEEE OUI registry covering common consumer electronics, IoT
// devices, and infrastructure prefixes — the goal is "show something
// useful for ~80% of home LAN devices" not "be exhaustive".
//
// Why not embed the full IEEE OUI list:
//   - The full registry is 4-5 MB of CSV, dominated by enterprise vendors
//     irrelevant to home routers.
//   - The curated subset below fits in <10 KB and covers the brands users
//     actually see (Apple, Xiaomi, Samsung, Huawei, Google, Raspberry Pi,
//     Realtek, Intel, common camera / smart-home OUIs, Docker, etc.).
//   - Downstream consumers who want exhaustive lookups can replace this
//     with their own table by setting the OUIDatabase variable below.
//
// MAC parsing accepts the canonical "aa:bb:cc:..." form (case-insensitive)
// and the no-separator "aabbcc..." form. Locally-administered addresses
// (second-least-significant bit of the first octet set) are deliberately
// not looked up — those are random, not real OUIs — and return "" so the
// UI shows "—" rather than a misleading guess.
//
// Stable since v1.2.0.
func LookupVendor(mac string) string {
	prefix, locally := parseOUI(mac)
	if prefix == "" || locally {
		return ""
	}
	if name, ok := OUIDatabase[prefix]; ok {
		return name
	}
	return ""
}

// parseOUI normalizes a MAC string to the 6-hex-char OUI prefix
// (uppercase, no separators). Returns ("", false) for unparseable input.
// The boolean is true when the MAC's locally-administered bit is set,
// in which case the OUI is meaningless (it's a privacy / random MAC).
func parseOUI(mac string) (string, bool) {
	if len(mac) < 8 {
		return "", false
	}
	// Strip separators in one pass; ASCII-only, so byte-level is fine.
	var b strings.Builder
	b.Grow(12)
	for i := 0; i < len(mac); i++ {
		c := mac[i]
		switch {
		case c >= '0' && c <= '9', c >= 'A' && c <= 'F':
			b.WriteByte(c)
		case c >= 'a' && c <= 'f':
			b.WriteByte(c - 32) // toUpper
		case c == ':' || c == '-' || c == '.':
			// separator, skip
		default:
			return "", false
		}
		if b.Len() >= 6 {
			break
		}
	}
	if b.Len() != 6 {
		return "", false
	}
	prefix := b.String()
	// Locally-administered: bit 1 of the first octet (e.g. 02:..., 06:...,
	// 0A:..., AA:..., DA:...). iOS / Android private WiFi addresses always
	// fall in here.
	first, err := hexByte(prefix[0], prefix[1])
	if err != nil {
		return "", false
	}
	return prefix, first&0x02 != 0
}

func hexByte(hi, lo byte) (byte, error) {
	v, ok := hexNib(hi)
	if !ok {
		return 0, errBadHex
	}
	w, ok := hexNib(lo)
	if !ok {
		return 0, errBadHex
	}
	return v<<4 | w, nil
}

func hexNib(c byte) (byte, bool) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', true
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10, true
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, true
	}
	return 0, false
}

var errBadHex = stringError("bad hex")

type stringError string

func (s stringError) Error() string { return string(s) }
