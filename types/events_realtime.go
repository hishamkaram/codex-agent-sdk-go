package types

import "encoding/json"

// --- v0.2.0 expansion: realtime-conversation events ---

// ThreadRealtimeStarted is emitted when the server begins a realtime
// (voice/audio) conversation. Wire method: "thread/realtime/started".
type ThreadRealtimeStarted struct {
	ThreadID string          `json:"thread_id"`
	Params   json.RawMessage `json:"params,omitempty"`
}

func (*ThreadRealtimeStarted) isThreadEvent()      {}
func (*ThreadRealtimeStarted) EventMethod() string { return "thread/realtime/started" }

// ThreadRealtimeClosed is emitted when a realtime conversation terminates.
// Wire method: "thread/realtime/closed".
type ThreadRealtimeClosed struct {
	ThreadID string          `json:"thread_id"`
	Params   json.RawMessage `json:"params,omitempty"`
}

func (*ThreadRealtimeClosed) isThreadEvent()      {}
func (*ThreadRealtimeClosed) EventMethod() string { return "thread/realtime/closed" }

// ThreadRealtimeError is emitted on realtime-conversation errors.
// Wire method: "thread/realtime/error".
type ThreadRealtimeError struct {
	ThreadID string          `json:"thread_id"`
	Params   json.RawMessage `json:"params,omitempty"`
}

func (*ThreadRealtimeError) isThreadEvent()      {}
func (*ThreadRealtimeError) EventMethod() string { return "thread/realtime/error" }

// ThreadRealtimeItemAdded is emitted when a realtime conversation adds a
// new item (audio/text fragment). Wire method: "thread/realtime/itemAdded".
type ThreadRealtimeItemAdded struct {
	ThreadID string          `json:"thread_id"`
	Params   json.RawMessage `json:"params,omitempty"`
}

func (*ThreadRealtimeItemAdded) isThreadEvent()      {}
func (*ThreadRealtimeItemAdded) EventMethod() string { return "thread/realtime/itemAdded" }

// ThreadRealtimeOutputAudioDelta is emitted as the server streams audio
// output chunks during a realtime conversation.
// Wire method: "thread/realtime/outputAudio/delta".
type ThreadRealtimeOutputAudioDelta struct {
	ThreadID string          `json:"thread_id"`
	Params   json.RawMessage `json:"params,omitempty"`
}

func (*ThreadRealtimeOutputAudioDelta) isThreadEvent() {}
func (*ThreadRealtimeOutputAudioDelta) EventMethod() string {
	return "thread/realtime/outputAudio/delta"
}

// ThreadRealtimeSdp carries WebRTC session-description exchanges.
// Wire method: "thread/realtime/sdp".
type ThreadRealtimeSdp struct {
	ThreadID string          `json:"thread_id"`
	Params   json.RawMessage `json:"params,omitempty"`
}

func (*ThreadRealtimeSdp) isThreadEvent()      {}
func (*ThreadRealtimeSdp) EventMethod() string { return "thread/realtime/sdp" }

// ThreadRealtimeTranscriptDelta streams the transcript of a realtime
// conversation. Wire method: "thread/realtime/transcript/delta".
type ThreadRealtimeTranscriptDelta struct {
	ThreadID string          `json:"thread_id"`
	Params   json.RawMessage `json:"params,omitempty"`
}

func (*ThreadRealtimeTranscriptDelta) isThreadEvent() {}
func (*ThreadRealtimeTranscriptDelta) EventMethod() string {
	return "thread/realtime/transcript/delta"
}

// ThreadRealtimeTranscriptDone is emitted when transcription for a
// realtime turn completes. Wire method: "thread/realtime/transcript/done".
type ThreadRealtimeTranscriptDone struct {
	ThreadID string          `json:"thread_id"`
	Params   json.RawMessage `json:"params,omitempty"`
}

func (*ThreadRealtimeTranscriptDone) isThreadEvent() {}
func (*ThreadRealtimeTranscriptDone) EventMethod() string {
	return "thread/realtime/transcript/done"
}
