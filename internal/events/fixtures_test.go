package events

import (
	"bufio"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/hishamkaram/codex-agent-sdk-go/internal/jsonrpc"
	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

// TestFixtureReplay_SpikeTranscript walks the full codex-integration spike
// transcript captured against a real `codex app-server` v0.121.0 and pushes
// every server→client notification through ParseEvent. Asserts:
//
//  1. Every inbound rx row that carries a "method" (notification) parses
//     without error.
//  2. Known method names produce a concrete typed event (not UnknownEvent).
//  3. Unknown method names produce UnknownEvent with the raw payload
//     preserved — forward-compat hatch is exercised.
//  4. Inbound rx rows with an "id" (responses / server-requests) are NOT
//     fed to ParseEvent — those go through the demux response/server-req
//     path and are not this test's concern.
//
// The transcript has 523 rows and exercises real notification shapes
// including thread/started, mcpServer/startupStatus/updated (unknown),
// and configWarning (unknown).
func TestFixtureReplay_SpikeTranscript(t *testing.T) {
	t.Parallel()

	f, err := os.Open("testdata/spike-transcript.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	// Per-method counters for a summary at the end.
	known := map[string]int{}
	unknown := map[string]int{}
	parseErrs := 0

	scanner := bufio.NewScanner(f)
	// The spike transcript has some long lines (thread/started payload is
	// ~1 KiB); bump the scanner buffer generously.
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)

	// Row envelope from send.py transcripts.
	type row struct {
		Dir string          `json:"dir"`
		Msg json.RawMessage `json:"msg"`
	}

	for scanner.Scan() {
		var r row
		if err := json.Unmarshal(scanner.Bytes(), &r); err != nil {
			t.Fatalf("unmarshal row: %v", err)
		}
		if r.Dir != "rx" {
			continue // skip tx, note, stderr, rx_raw
		}

		// Peek at shape — only notifications (method, no id) are our target.
		var shape struct {
			ID     *uint64         `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if err := json.Unmarshal(r.Msg, &shape); err != nil {
			t.Fatalf("unmarshal msg: %v", err)
		}
		if shape.ID != nil || shape.Method == "" {
			continue // response or server-request — not this test's scope
		}

		ev, err := ParseEvent(jsonrpc.Notification{
			Method: shape.Method,
			Params: shape.Params,
		})
		if err != nil {
			parseErrs++
			t.Errorf("ParseEvent(%q) error: %v", shape.Method, err)
			continue
		}

		if _, isUnknown := ev.(*types.UnknownEvent); isUnknown {
			unknown[shape.Method]++
			// Raw payload must be preserved.
			u := ev.(*types.UnknownEvent)
			if len(shape.Params) > 0 && len(u.Params) == 0 {
				t.Errorf("UnknownEvent for %q dropped params", shape.Method)
			}
		} else {
			known[shape.Method]++
			// Note: ev.EventMethod() is NOT required to equal the wire
			// method — the SDK normalizes per-item-type delta wire methods
			// (e.g. "item/agentMessage/delta") into a canonical
			// ItemUpdated event whose EventMethod is "item/updated". That
			// normalization is intentional; see parser.go.
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scanner: %v", err)
	}

	if parseErrs > 0 {
		t.Fatalf("%d parse errors across fixture replay", parseErrs)
	}

	// Sanity: the transcript includes at least one thread/started — a known
	// method we explicitly support. If this breaks, the fixture has been
	// replaced with an unrelated file.
	if known["thread/started"] == 0 {
		t.Fatal("expected at least one thread/started in fixture")
	}

	t.Logf("replayed fixture: %d known + %d unknown methods", sumCounts(known), sumCounts(unknown))
	for m, n := range known {
		t.Logf("  known  %-40s %d", m, n)
	}
	for m, n := range unknown {
		t.Logf("  unknown %-40s %d", m, n)
	}

	// Hard invariant: the captured transcript represents a known subset of
	// the wire protocol. Every method in it MUST resolve to a typed event.
	// When codex adds a new notification method (future 0.12x release),
	// this assertion fires — at which point the contract is:
	//
	//   1. Re-run `make regen-schema` to refresh testdata/schema/.
	//   2. Add the new method's types in types/events.go.
	//   3. Add the parser switch arm in parser.go.
	//   4. Re-capture the fixture transcript (or accept the old fixture if
	//      the new method isn't in it yet).
	//
	// See docs/wire-protocol.md for the full contract.
	if len(unknown) > 0 {
		methods := make([]string, 0, len(unknown))
		for m := range unknown {
			methods = append(methods, m)
		}
		t.Fatalf("fixture contains %d wire methods not typed by the SDK: %v\n"+
			"See the comment above this assertion for the update protocol.",
			len(unknown), methods)
	}
}

// TestFixtureReplay_ItemsExercised confirms that among the events that are
// ItemStarted/ItemCompleted in the fixture, the inner item payloads all
// parse cleanly (either into a known ThreadItem or a *UnknownItem).
func TestFixtureReplay_ItemsExercised(t *testing.T) {
	t.Parallel()

	f, err := os.Open("testdata/spike-transcript.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)

	itemMethods := 0
	itemTypes := map[string]int{}

	type row struct {
		Dir string          `json:"dir"`
		Msg json.RawMessage `json:"msg"`
	}

	for scanner.Scan() {
		var r row
		if err := json.Unmarshal(scanner.Bytes(), &r); err != nil {
			t.Fatal(err)
		}
		if r.Dir != "rx" {
			continue
		}
		var shape struct {
			ID     *uint64         `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		_ = json.Unmarshal(r.Msg, &shape)
		if shape.ID != nil {
			continue
		}
		if !strings.HasPrefix(shape.Method, "item/") {
			continue
		}

		ev, err := ParseEvent(jsonrpc.Notification{Method: shape.Method, Params: shape.Params})
		if err != nil {
			t.Fatalf("parse item event %q: %v", shape.Method, err)
		}
		itemMethods++

		switch e := ev.(type) {
		case *types.ItemStarted:
			itemTypes[e.Item.ItemType()]++
		case *types.ItemCompleted:
			itemTypes[e.Item.ItemType()]++
		}
	}

	t.Logf("exercised %d item events, types: %v", itemMethods, itemTypes)
}

func sumCounts(m map[string]int) int {
	n := 0
	for _, v := range m {
		n += v
	}
	return n
}
