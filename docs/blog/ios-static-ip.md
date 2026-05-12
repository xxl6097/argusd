# OpenWrt + iPhone: the three ways "set static IP" silently fails

> A debugging story — published 2026-05-12. The reproducer is the
> Argus v0.15.x development cycle; all log excerpts are real.

If you run OpenWrt (or any vendor fork of it) at home and have ever
tried to pin an iPhone to a specific IP by editing `/etc/config/dhcp`,
you've probably seen this:

1. You add `dhcp.foo=host` with the right MAC and IP
2. `uci commit dhcp && /etc/init.d/dnsmasq reload`
3. iPhone keeps its old IP **for up to 12 hours**
4. You give up, set a static IP on the phone manually

This post walks through **three independent failure modes** that
produce the same symptom, each of which took an afternoon to diagnose
in a real home setup. None of them are your config's fault.

---

## Setup

- MediaTek MT7981-based router (SoC used in many Xiaomi / GL.iNet
  units) running C-Life vendor firmware, which is OpenWrt 22.03-ish
  underneath but with vendor `ahsapd` replacing `hostapd`
- dnsmasq 2.89 handling IPv4 DHCP
- `/etc/config/dhcp` is uci-managed; the "official" way to add a
  reservation
- Client: iPhone 17 running iOS 18
- Before the test: iPhone holds a dynamic lease of `192.168.1.213`
- Goal: pin it to `192.168.1.2`

All three failures ended up being **client-side state caching**,
**router-side stale ARP**, or **second DHCP server on the LAN**,
respectively.

---

## Failure #1 — iOS re-requests its old lease and refuses to let go

### What the logs show

After `uci commit + dnsmasq reload`, watching `logread -f` while the
iPhone is briefly kicked off WiFi:

```
DHCPREQUEST(br-lan) 192.168.1.213 ba:79:97:73:89:8d
DHCPNAK(br-lan) 192.168.1.213 ba:79:97:73:89:8d static lease available
DHCPDISCOVER(br-lan) ba:79:97:73:89:8d
DHCPOFFER(br-lan) 192.168.1.2 ba:79:97:73:89:8d
DHCPREQUEST(br-lan) 192.168.1.213 ba:79:97:73:89:8d
DHCPNAK(br-lan) 192.168.1.213 ba:79:97:73:89:8d wrong server-ID
```

dnsmasq is doing the right thing: the iPhone asks for its old IP, gets
NAK'd because a static lease exists, falls back to DISCOVER, gets
OFFER'd `192.168.1.2` — and **then asks for `192.168.1.213` again**.

### Why

iOS caches the previous DHCP state aggressively. When WiFi briefly
disconnects and reconnects, the DHCP client goes into RENEWING state
(RFC 2131 §4.3.2) and re-requests the **cached** address even though
the lease timer says it should. If the NAK comes back fast, iOS
**sometimes** falls back to DISCOVER, but when the reconnect is fast
enough (< 3 seconds), iOS gives up after the second NAK and just
continues using the cached IP at the IP layer without a valid DHCP
ACK. Welcome to undefined behavior.

The router sees the iPhone as "online" because WiFi is associated,
but from DHCP's point of view the phone has no active lease.

### Fix

The disconnect has to last **long enough** for iOS's state machine to
time out and drop the cached lease. Empirically, 30 seconds is
enough; 2 seconds is not. The MTK vendor `ahsapd.roaming
staDisconnect` call takes a `dismissTime` integer argument — bump it
from 2 to 30:

```json
{"macAddress":"ba:79:97:73:89:8d","dismissTime":30}
```

(But see Failure #3 below — on some firmware revisions this call
silently no-ops regardless of `dismissTime`.)

---

## Failure #2 — the router's ARP table lies to iOS

Even after iOS does get a fresh DHCP ACK for `192.168.1.2`, a
separate failure mode keeps it using the old IP.

### What the ARP table shows

```
$ ip neigh
192.168.1.213 dev br-lan lladdr ba:79:97:73:89:8d REACHABLE
192.168.1.2   dev br-lan  FAILED
```

The router still has a cached ARP mapping of `192.168.1.213` to the
iPhone's MAC. When the iPhone sends an ARP probe for
`192.168.1.213` (part of its "is my cached IP still OK?" check
before dropping it), the router **confirms** the mapping — because
it hasn't been told to forget.

iOS takes that as "the IP is still usable" and silently reverts to
it.

### Fix

Before disconnecting the client, flush its ARP entries:

```go
// argusweb/dhcp.go, part of applyDHCPChanges:
// Read /proc/net/arp, find every entry whose HW addr matches our MAC,
// delete it via `ip neigh del <ip> dev <iface>`.
```

This is cheap (microseconds) but load-bearing — without it, Failure
#2 defeats Failure #1's fix.

---

## Failure #3 — a second DHCP server on your LAN

This one is the most destructive and the hardest to diagnose, because
**nothing in the router's own logs points to it**.

### What the symptom looks like

- After all fixes above, some devices come back with gateway set to
  `192.168.1.11` instead of `192.168.1.1`
- The iPhone's DHCP negotiation shows `DHCPNAK ... wrong server-ID`
  at random — which is RFC-speak for "the DHCP server this client
  thinks it's talking to isn't the one that answered this time"
- The static IP reservation on the main router **works for some
  devices and not others**, with the split being completely
  unpredictable

### Why

`192.168.1.11` is a Raspberry Pi on the LAN running **iStoreOS**, an
OpenWrt fork pitched at NAS / bypass-router use cases. By default,
iStoreOS enables `dnsmasq` as a full DHCP server on `br-lan`.

Two DHCP servers on the same broadcast domain race. Whoever's OFFER
reaches the client first wins. iStoreOS advertises **itself** as the
gateway (option 3 = `192.168.1.11`), so any client whose OFFER was
iStoreOS's ends up routing through the Pi instead of the main router.

### Fix

On the iStoreOS box:

```sh
uci set dhcp.lan.ignore=1
uci commit dhcp
/etc/init.d/dnsmasq restart
```

That tells dnsmasq not to serve DHCP on `lan`. DNS caching and local
hostname resolution still work (useful for a NAS) — only DHCP is
disabled.

**How to diagnose** without knowing the second server exists:

```sh
# On the main router: watch who's actually answering DHCP
tcpdump -i br-lan -n 'udp port 67 or udp port 68' | \
    grep -E 'BOOTP|DHCP'
# If you see OFFER packets from a source IP that isn't your router,
# that's your rogue server.
```

Or, simpler: run `ip neigh` on each device that's misbehaving and
check its default route. If the gateway isn't `192.168.1.1`, you have
a second DHCP server.

---

## Defense in depth

Argus applies all three fixes in order when you save a static IP
through its Web UI (v0.15.7+):

```
1. uci set + uci commit                     → write the reservation
2. /etc/init.d/dnsmasq reload               → make dnsmasq re-read
3. prune /tmp/dhcp.leases for the MAC       → drop stale lease
4. ip neigh del <oldIP> dev <iface>         → flush router ARP cache
5. ubus call ahsapd.roaming staDisconnect \
      {macAddress, dismissTime:30}          → kick station 30s
6. optional: /sbin/wifi reload              → nuclear: kick everyone
7. return applyReport{reloaded, pruned,
      arp_flushed, kicked, wifi_restarted}  → UI shows what happened
```

The `applyReport` surfaces in a toast so you can tell at a glance
which of the six steps actually executed. If `kicked` is empty even
though the request succeeded, you're probably on a vendor firmware
that silently no-ops `staDisconnect` — check the next section.

### Vendor firmwares with silent no-op disconnects

On MediaTek C-Life (and a few other MTK-based vendor builds),
`ubus call ahsapd.roaming staDisconnect` returns exit 0 **but does
not actually disconnect the station**. There's no `Del Sta` or
`Deauth` kernel event afterward. As far as we can tell, the method
is wired for roaming between multiple APs and silently drops the
call in single-AP setups.

Argus v0.15.8 added an opt-in "full WiFi reload" checkbox in the
static-IP save dialog for this case. It runs `wifi reload`
(or `/etc/init.d/ahsapd restart`) — every client on every radio
drops for 3-5 seconds and auto-reassociates. Heavy-handed, but
reliable, and only fires when the user checks the box.

---

## Takeaways

- **"Set a static IP" is three independent problems on a home LAN.**
  Fixing just one still looks broken.
- **iOS's DHCP client is unusually sticky.** Don't blame yourself.
  Disconnect duration, ARP state, and alternate DHCP responders all
  matter.
- **Rogue DHCP servers are the most common culprit** if you have
  "smart" devices on the LAN (Pis running iStoreOS, OpenClash
  bypasses, TrueNAS, etc.). `uci set dhcp.lan.ignore=1` on every
  box that isn't your designated gateway.
- **Watch real packets, not GUIs.** `tcpdump` and `logread` told
  the truth; LuCI's "Active DHCP leases" table hid all three
  failures.

---

## Try it

Argus is MIT-licensed, single-binary Go, cross-compiles to
mipsle/arm64/amd64, works on stock OpenWrt and most MediaTek vendor
forks. No cgo, no agents on clients.

- Source: <https://github.com/xxl6097/argus>
- CLI binaries: <https://github.com/xxl6097/argusd/releases>
- Related deep-dives:
  [`ONLINE.md`](../../ONLINE.md),
  [`OFFLINE.md`](../../OFFLINE.md),
  [`STABILITY.md`](../../STABILITY.md)
