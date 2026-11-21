package threadexec

import (
	"sync"

	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

// Registry owns the connection's canonical records. It can acquire a
// record lock under mu. Operations on records never acquire mu.
type Registry struct {
	mu      sync.Mutex
	entries map[string]entry
	latest  string
}

type entry struct {
	record      *Record
	provisional bool
}

func NewRegistry() *Registry {
	return &Registry{entries: make(map[string]entry)}
}

func (r *Registry) Latest() string {
	if r == nil {
		return ""
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.latest
}

func (r *Registry) Lookup(id string) *Record {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.entries[CanonicalID(id)].record
}

// Register binds a returned handle to its canonical record. The provider's
// wire spelling is kept separately for the SessionID projection.
func (r *Registry) Register(id string, record *Record, connected func() bool) *Record {
	if r == nil {
		if record != nil {
			record.Close()
		}
		return record
	}
	key := CanonicalID(id)
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.entries == nil {
		if record != nil {
			record.Close()
		}
		return record
	}
	if existing := r.entries[key].record; existing != nil {
		existing.reconcileExternalActivity(record)
		record = existing
	} else {
		if record == nil {
			record = NewRecord(false)
		}
		if !connected() {
			record.Close()
		}
	}
	r.entries[key] = entry{record: record}
	r.latest = id
	return record
}

// BeginResume keeps ordered activity for a provisional identity before the
// provider acknowledges the request.
func (r *Registry) BeginResume(id string, closed func() bool) *Record {
	if r == nil {
		return nil
	}
	key := CanonicalID(id)
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.entries == nil || closed() {
		return nil
	}
	current := r.entries[key]
	if current.record == nil {
		current = entry{record: NewRecord(false), provisional: true}
		r.entries[key] = current
	}
	record := current.record
	record.mu.Lock()
	defer record.mu.Unlock()
	record.resuming++
	return record
}

func (r *Registry) EndResume(id string, record *Record) {
	key := CanonicalID(id)
	r.mu.Lock()
	defer r.mu.Unlock()
	record.mu.Lock()
	defer record.mu.Unlock()
	record.resuming--
	if current := r.entries[key]; record.resuming == 0 && current.record == record && current.provisional {
		delete(r.entries, key)
	}
	record.signalLocked()
}

// ReconcileRejectedResume retains active evidence on the provider's
// returned identity even when policy checks reject the new handle.
func (r *Registry) ReconcileRejectedResume(id string, incoming *Record) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if found := r.entries[CanonicalID(id)].record; found != nil {
		found.reconcileExternalActivity(incoming)
	}
}

func (r *Registry) Archive(id string) {
	if r == nil {
		return
	}
	key := CanonicalID(id)
	r.mu.Lock()
	defer r.mu.Unlock()
	if retained := r.entries[key].record; retained != nil {
		retained.invalidate()
	}
	if CanonicalID(r.latest) == key {
		r.latest = ""
	}
}

func (r *Registry) Invalidate(id string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if id != "" {
		if record := r.entries[CanonicalID(id)].record; record != nil {
			record.invalidate()
		}
		return
	}
	for _, current := range r.entries {
		current.record.invalidate()
	}
}

func (r *Registry) Observe(id string, event types.ThreadEvent) bool {
	record := r.Lookup(id)
	return record != nil && record.Observe(event)
}

// CloseRecords handles loss of the event source. Close additionally ends
// registration and drops the registry's references to the closed records.
func (r *Registry) CloseRecords() {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closeRecordsLocked()
}

func (r *Registry) Close() {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closeRecordsLocked()
	r.entries = nil
	r.latest = ""
}

func (r *Registry) closeRecordsLocked() {
	for _, current := range r.entries {
		current.record.Close()
	}
}
