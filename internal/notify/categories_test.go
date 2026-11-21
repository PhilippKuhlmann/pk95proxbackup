package notify

import "testing"

func TestChannelWants(t *testing.T) {
	s := Settings{MinSeverity: "warning", Channels: map[string]ChannelPrefs{
		ChannelTelegram: {MinSeverity: "critical", Off: []string{"changes", "report"}},
		ChannelHook:     {MinSeverity: "info", Off: []string{"updates"}, Levels: map[string]string{"updates": "problems", "reach": "off", "backups": "all"}},
	}}
	for _, c := range []struct {
		ch   string
		m    Message
		want bool
	}{
		{ChannelMail, Message{Severity: "warning", Category: "changes"}, true},
		{ChannelMail, Message{Severity: "info", Category: "backups"}, false},         // below the general level
		{ChannelMail, Message{Severity: "error", Category: "updates"}, true},         // "error" counts like critical
		{ChannelTelegram, Message{Severity: "critical", Category: "changes"}, false}, // switched off
		{ChannelTelegram, Message{Severity: "warning", Category: "disks"}, false},    // own level: only errors
		{ChannelTelegram, Message{Severity: "critical", Category: "disks"}, true},
		{ChannelMail, Message{Severity: "info", Category: "report"}, true},      // the report ignores the level
		{ChannelTelegram, Message{Severity: "info", Category: "report"}, false}, // but not the switch
		{ChannelTelegram, Message{Severity: "info"}, true},                      // test messages always
		{ChannelHook, Message{Severity: "info", Category: "updates"}, false},    // only problems (levels win over off)
		{ChannelHook, Message{Severity: "warning", Category: "updates"}, true},
		{ChannelHook, Message{Severity: "critical", Category: "reach"}, false}, // off: not even errors
		{ChannelHook, Message{Severity: "info", Category: "backups"}, true},
	} {
		if got := s.ChannelWants(c.ch, c.m); got != c.want {
			t.Errorf("%s %+v: got %v", c.ch, c.m, got)
		}
	}
}
