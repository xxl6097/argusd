package argus

import "testing"

func TestEventKindString(t *testing.T) {
	cases := []struct {
		k    EventKind
		want string
	}{
		{EventOnline, "ONLINE"},
		{EventOffline, "OFFLINE"},
		{EventChange, "CHANGE"},
		{EventKind(0), "UNKNOWN"},
		{EventKind(99), "UNKNOWN"},
	}
	for _, c := range cases {
		if got := c.k.String(); got != c.want {
			t.Errorf("EventKind(%d).String() = %q, want %q", c.k, got, c.want)
		}
	}
}

func TestEventKindLabel(t *testing.T) {
	cases := []struct {
		k    EventKind
		want string
	}{
		{EventOnline, "设备上线"},
		{EventOffline, "设备离线"},
		{EventChange, "状态变更"},
		{EventKind(0), "未知事件"},
	}
	for _, c := range cases {
		if got := c.k.Label(); got != c.want {
			t.Errorf("EventKind(%d).Label() = %q, want %q", c.k, got, c.want)
		}
	}
}
