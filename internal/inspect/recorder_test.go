package inspect

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func makeEntry(id string, method string, path string, status int) Entry {
	return Entry{
		ID:              id,
		Method:          method,
		Path:            path,
		RequestHeaders:  map[string]string{"Host": "example.com"},
		Status:          status,
		ResponseHeaders: map[string]string{"Content-Type": "application/json"},
		StartedAt:       time.Now(),
		Duration:        10 * time.Millisecond,
	}
}

func TestRecorder_Record(t *testing.T) {
	r := NewRecorder(10)
	r.Record(makeEntry("1", "GET", "/", 200))

	assert.Equal(t, 1, r.Len())
	entries := r.Entries()
	assert.Equal(t, "1", entries[0].ID)
}

func TestRecorder_RingBuffer(t *testing.T) {
	r := NewRecorder(3)
	r.Record(makeEntry("1", "GET", "/a", 200))
	r.Record(makeEntry("2", "GET", "/b", 200))
	r.Record(makeEntry("3", "GET", "/c", 200))
	r.Record(makeEntry("4", "GET", "/d", 200))

	assert.Equal(t, 3, r.Len())
	entries := r.Entries()
	assert.Equal(t, "2", entries[0].ID)
	assert.Equal(t, "4", entries[2].ID)
}

func TestRecorder_Get(t *testing.T) {
	r := NewRecorder(10)
	r.Record(makeEntry("abc", "POST", "/items", 201))

	e, ok := r.Get("abc")
	assert.True(t, ok)
	assert.Equal(t, "POST", e.Method)
	assert.Equal(t, 201, e.Status)

	_, ok = r.Get("nonexistent")
	assert.False(t, ok)
}

func TestRecorder_Clear(t *testing.T) {
	r := NewRecorder(10)
	r.Record(makeEntry("1", "GET", "/", 200))
	r.Record(makeEntry("2", "GET", "/", 200))
	r.Clear()

	assert.Equal(t, 0, r.Len())
}

func TestRecorder_Subscribe(t *testing.T) {
	r := NewRecorder(10)
	ch := r.Subscribe()

	go func() {
		r.Record(makeEntry("live", "GET", "/stream", 200))
	}()

	select {
	case e := <-ch:
		assert.Equal(t, "live", e.ID)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for entry")
	}

	r.Unsubscribe(ch)
}

func TestRecorder_Filter_Method(t *testing.T) {
	r := NewRecorder(10)
	r.Record(makeEntry("1", "GET", "/a", 200))
	r.Record(makeEntry("2", "POST", "/b", 201))
	r.Record(makeEntry("3", "GET", "/c", 200))

	filtered := r.Filter("GET", "", 0, 0)
	assert.Len(t, filtered, 2)
	assert.Equal(t, "1", filtered[0].ID)
	assert.Equal(t, "3", filtered[1].ID)
}

func TestRecorder_Filter_Status(t *testing.T) {
	r := NewRecorder(10)
	r.Record(makeEntry("1", "GET", "/a", 200))
	r.Record(makeEntry("2", "GET", "/b", 404))
	r.Record(makeEntry("3", "GET", "/c", 500))

	filtered := r.Filter("", "", 400, 599)
	assert.Len(t, filtered, 2)
}

func TestRecorder_Filter_Path(t *testing.T) {
	r := NewRecorder(10)
	r.Record(makeEntry("1", "GET", "/api/users", 200))
	r.Record(makeEntry("2", "GET", "/api/items", 200))

	filtered := r.Filter("", "/api/users", 0, 0)
	assert.Len(t, filtered, 1)
	assert.Equal(t, "1", filtered[0].ID)
}

func TestRecorder_EntriesReturnsACopy(t *testing.T) {
	r := NewRecorder(10)
	r.Record(makeEntry("1", "GET", "/", 200))

	entries := r.Entries()
	entries[0].ID = "modified"

	original := r.Entries()
	assert.Equal(t, "1", original[0].ID)
}

func TestRecorder_DefaultMax(t *testing.T) {
	r := NewRecorder(0)
	assert.NotNil(t, r)

	for i := 0; i < 600; i++ {
		r.Record(makeEntry(fmt.Sprintf("%d", i), "GET", "/", 200))
	}
	assert.Equal(t, defaultMaxEntries, r.Len())
}

func TestRecorder_ConcurrentAccess(t *testing.T) {
	r := NewRecorder(100)
	done := make(chan struct{})

	// Writer
	go func() {
		for i := 0; i < 200; i++ {
			r.Record(makeEntry(fmt.Sprintf("%d", i), "GET", "/", 200))
		}
		close(done)
	}()

	// Reader
	for range 50 {
		_ = r.Entries()
		_ = r.Len()
	}

	<-done
	require.LessOrEqual(t, r.Len(), 100)
}
