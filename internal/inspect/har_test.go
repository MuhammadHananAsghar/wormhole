package inspect

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildHAR_Empty(t *testing.T) {
	har := BuildHAR(nil)
	assert.Equal(t, "1.2", har.Log.Version)
	assert.Equal(t, "Wormhole", har.Log.Creator.Name)
	assert.Empty(t, har.Log.Entries)
}

func TestBuildHAR_WithEntries(t *testing.T) {
	entries := []Entry{
		{
			ID:              "1",
			Method:          "GET",
			Path:            "/api/users",
			RequestHeaders:  map[string]string{"Accept": "application/json"},
			Status:          200,
			ResponseHeaders: map[string]string{"Content-Type": "application/json"},
			ResponseBody:    []byte(`[{"id":1}]`),
			StartedAt:       time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC),
			Duration:        15 * time.Millisecond,
		},
		{
			ID:             "2",
			Method:         "POST",
			Path:           "/api/items",
			RequestHeaders: map[string]string{"Content-Type": "application/json"},
			RequestBody:    []byte(`{"name":"test"}`),
			Status:         201,
			StartedAt:      time.Date(2025, 1, 1, 12, 0, 1, 0, time.UTC),
			Duration:       8 * time.Millisecond,
		},
	}

	har := BuildHAR(entries)
	require.Len(t, har.Log.Entries, 2)

	first := har.Log.Entries[0]
	assert.Equal(t, "GET", first.Request.Method)
	assert.Equal(t, "/api/users", first.Request.URL)
	assert.Equal(t, 200, first.Response.Status)
	assert.Equal(t, "OK", first.Response.StatusText)
	assert.Equal(t, float64(15), first.Time)
	assert.Equal(t, `[{"id":1}]`, first.Response.Content.Text)
	assert.Equal(t, 10, first.Response.Content.Size)

	second := har.Log.Entries[1]
	assert.Equal(t, "POST", second.Request.Method)
	assert.Equal(t, 201, second.Response.Status)
	assert.Equal(t, "Created", second.Response.StatusText)
	assert.Equal(t, 15, second.Request.BodySize)
}

func TestStatusText(t *testing.T) {
	tests := []struct {
		code int
		text string
	}{
		{200, "OK"},
		{201, "Created"},
		{404, "Not Found"},
		{500, "Internal Server Error"},
		{418, ""},
	}
	for _, tt := range tests {
		assert.Equal(t, tt.text, statusText(tt.code))
	}
}
