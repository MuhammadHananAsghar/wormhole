package inspect

import (
	"sync"
	"time"
)

const defaultMaxEntries = 500

// Entry represents a captured request/response pair.
type Entry struct {
	ID            string            `json:"id"`
	Method        string            `json:"method"`
	Path          string            `json:"path"`
	RequestHeaders  map[string]string `json:"request_headers"`
	RequestBody   []byte            `json:"request_body,omitempty"`
	Status        int               `json:"status"`
	ResponseHeaders map[string]string `json:"response_headers"`
	ResponseBody  []byte            `json:"response_body,omitempty"`
	StartedAt     time.Time         `json:"started_at"`
	Duration      time.Duration     `json:"duration_ns"`
	ContentType   string            `json:"content_type"`
}

// Recorder captures request/response pairs in a ring buffer.
type Recorder struct {
	mu         sync.RWMutex
	entries    []Entry
	max        int
	listeners  []chan Entry
	listenerMu sync.Mutex
}

// NewRecorder creates a recorder with the given max entries.
func NewRecorder(max int) *Recorder {
	if max <= 0 {
		max = defaultMaxEntries
	}
	return &Recorder{
		entries: make([]Entry, 0, max),
		max:     max,
	}
}

// Record adds an entry and notifies all listeners.
func (r *Recorder) Record(e Entry) {
	r.mu.Lock()
	if len(r.entries) >= r.max {
		r.entries = r.entries[1:]
	}
	r.entries = append(r.entries, e)
	r.mu.Unlock()

	r.listenerMu.Lock()
	for _, ch := range r.listeners {
		select {
		case ch <- e:
		default:
			// drop if listener is slow
		}
	}
	r.listenerMu.Unlock()
}

// Entries returns a copy of all recorded entries.
func (r *Recorder) Entries() []Entry {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Entry, len(r.entries))
	copy(out, r.entries)
	return out
}

// Len returns the number of recorded entries.
func (r *Recorder) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.entries)
}

// Get returns an entry by ID.
func (r *Recorder) Get(id string) (Entry, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, e := range r.entries {
		if e.ID == id {
			return e, true
		}
	}
	return Entry{}, false
}

// Subscribe returns a channel that receives new entries as they are recorded.
// Call Unsubscribe to clean up.
func (r *Recorder) Subscribe() chan Entry {
	ch := make(chan Entry, 64)
	r.listenerMu.Lock()
	r.listeners = append(r.listeners, ch)
	r.listenerMu.Unlock()
	return ch
}

// Unsubscribe removes a listener channel.
func (r *Recorder) Unsubscribe(ch chan Entry) {
	r.listenerMu.Lock()
	defer r.listenerMu.Unlock()
	for i, c := range r.listeners {
		if c == ch {
			r.listeners = append(r.listeners[:i], r.listeners[i+1:]...)
			close(ch)
			return
		}
	}
}

// Clear removes all recorded entries.
func (r *Recorder) Clear() {
	r.mu.Lock()
	r.entries = r.entries[:0]
	r.mu.Unlock()
}

// Filter returns entries matching the given criteria.
func (r *Recorder) Filter(method, path string, statusMin, statusMax int) []Entry {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var out []Entry
	for _, e := range r.entries {
		if method != "" && e.Method != method {
			continue
		}
		if path != "" && e.Path != path {
			continue
		}
		if statusMin > 0 && e.Status < statusMin {
			continue
		}
		if statusMax > 0 && e.Status > statusMax {
			continue
		}
		out = append(out, e)
	}
	return out
}
