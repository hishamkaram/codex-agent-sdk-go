package types

import "testing"

func TestEventMethod_EveryKnownEvent(t *testing.T) {
	t.Parallel()
	cases := []struct {
		event ThreadEvent
		want  string
	}{
		{&ThreadStarted{}, "thread/started"},
		{&TurnStarted{}, "turn/started"},
		{&TurnCompleted{}, "turn/completed"},
		{&TurnFailed{}, "turn/failed"},
		{&ItemStarted{}, "item/started"},
		{&ItemUpdated{}, "item/updated"},
		{&ItemCompleted{}, "item/completed"},
		{&TokenUsageUpdated{}, "thread/tokenUsage/updated"},
		{&CompactionEvent{}, "compaction_event"},
		{&ErrorEvent{}, "error"},
		{&UnknownEvent{Method: "future/event"}, "future/event"},
	}
	for _, c := range cases {
		c := c
		t.Run(c.want, func(t *testing.T) {
			t.Parallel()
			if got := c.event.EventMethod(); got != c.want {
				t.Fatalf("got %q, want %q", got, c.want)
			}
		})
	}
}
