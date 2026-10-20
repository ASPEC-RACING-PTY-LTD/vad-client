package agent

import (
	"testing"
	"time"
)

func TestUpdateDue(t *testing.T) {
	now := time.Now()
	ago := func(d time.Duration) time.Time { return now.Add(-d) }
	for _, c := range []struct {
		name    string
		last    time.Time
		visible bool
		reason  string
		want    bool
	}{
		{"never checked", time.Time{}, false, "timer", true},
		{"start after a check minutes ago", ago(5 * time.Minute), false, "start", false},
		{"start after two hours", ago(2 * time.Hour), false, "start", true},
		{"sync right after the start check, hidden", ago(time.Minute), false, "sync", false},
		{"hidden, five hours", ago(5 * time.Hour), false, "timer", false},
		{"hidden, six hours", ago(6 * time.Hour), false, "sync", true},
		{"open, 50 minutes", ago(50 * time.Minute), true, "timer", false},
		{"open, an hour", ago(time.Hour), true, "timer", true},
		{"window opened after two hours", ago(2 * time.Hour), true, "window", true},
	} {
		if got := updateDue(c.last, c.visible, c.reason, now); got != c.want {
			t.Errorf("%s: due = %v, want %v", c.name, got, c.want)
		}
	}
}
