package inspect

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"time"
)

// ReplayResult holds the result of replaying a request.
type ReplayResult struct {
	Status     int               `json:"status"`
	Headers    map[string]string `json:"headers"`
	Body       []byte            `json:"body,omitempty"`
	DurationMs float64           `json:"duration_ms"`
}

// Replay re-sends a captured request to the local server.
func Replay(localAddr string, entry Entry) (*ReplayResult, error) {
	url := fmt.Sprintf("http://%s%s", localAddr, entry.Path)

	var body io.Reader
	if len(entry.RequestBody) > 0 {
		body = bytes.NewReader(entry.RequestBody)
	}

	req, err := http.NewRequest(entry.Method, url, body)
	if err != nil {
		return nil, fmt.Errorf("creating replay request: %w", err)
	}

	for k, v := range entry.RequestHeaders {
		if k == "Host" || k == "host" {
			continue
		}
		req.Header.Set(k, v)
	}
	req.Host = localAddr

	start := time.Now()
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("replaying request: %w", err)
	}
	defer resp.Body.Close()
	duration := time.Since(start)

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading replay response: %w", err)
	}

	headers := make(map[string]string)
	for k := range resp.Header {
		headers[k] = resp.Header.Get(k)
	}

	return &ReplayResult{
		Status:     resp.StatusCode,
		Headers:    headers,
		Body:       respBody,
		DurationMs: float64(duration.Milliseconds()),
	}, nil
}
