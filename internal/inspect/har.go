package inspect

import (
	"time"
)

// HAR format types as per http://www.softwareishard.com/blog/har-12-spec/

// HAR is the top-level HAR container.
type HAR struct {
	Log HARLog `json:"log"`
}

// HARLog contains the log data.
type HARLog struct {
	Version string     `json:"version"`
	Creator HARCreator `json:"creator"`
	Entries []HAREntry `json:"entries"`
}

// HARCreator identifies the tool that created the log.
type HARCreator struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// HAREntry represents a single request/response pair.
type HAREntry struct {
	StartedDateTime string      `json:"startedDateTime"`
	Time            float64     `json:"time"`
	Request         HARRequest  `json:"request"`
	Response        HARResponse `json:"response"`
}

// HARRequest represents the HTTP request.
type HARRequest struct {
	Method      string      `json:"method"`
	URL         string      `json:"url"`
	HTTPVersion string      `json:"httpVersion"`
	Headers     []HARHeader `json:"headers"`
	HeadersSize int         `json:"headersSize"`
	BodySize    int         `json:"bodySize"`
}

// HARResponse represents the HTTP response.
type HARResponse struct {
	Status      int         `json:"status"`
	StatusText  string      `json:"statusText"`
	HTTPVersion string      `json:"httpVersion"`
	Headers     []HARHeader `json:"headers"`
	Content     HARContent  `json:"content"`
	HeadersSize int         `json:"headersSize"`
	BodySize    int         `json:"bodySize"`
}

// HARHeader represents a single header.
type HARHeader struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// HARContent represents the response body content.
type HARContent struct {
	Size     int    `json:"size"`
	MimeType string `json:"mimeType"`
	Text     string `json:"text,omitempty"`
}

// BuildHAR converts recorded entries into HAR format.
func BuildHAR(entries []Entry) *HAR {
	harEntries := make([]HAREntry, 0, len(entries))

	for _, e := range entries {
		reqHeaders := make([]HARHeader, 0, len(e.RequestHeaders))
		for k, v := range e.RequestHeaders {
			reqHeaders = append(reqHeaders, HARHeader{Name: k, Value: v})
		}

		respHeaders := make([]HARHeader, 0, len(e.ResponseHeaders))
		for k, v := range e.ResponseHeaders {
			respHeaders = append(respHeaders, HARHeader{Name: k, Value: v})
		}

		mimeType := e.ContentType
		if mimeType == "" {
			mimeType = e.ResponseHeaders["Content-Type"]
		}

		harEntries = append(harEntries, HAREntry{
			StartedDateTime: e.StartedAt.Format(time.RFC3339Nano),
			Time:            float64(e.Duration.Milliseconds()),
			Request: HARRequest{
				Method:      e.Method,
				URL:         e.Path,
				HTTPVersion: "HTTP/1.1",
				Headers:     reqHeaders,
				HeadersSize: -1,
				BodySize:    len(e.RequestBody),
			},
			Response: HARResponse{
				Status:      e.Status,
				StatusText:  statusText(e.Status),
				HTTPVersion: "HTTP/1.1",
				Headers:     respHeaders,
				Content: HARContent{
					Size:     len(e.ResponseBody),
					MimeType: mimeType,
					Text:     string(e.ResponseBody),
				},
				HeadersSize: -1,
				BodySize:    len(e.ResponseBody),
			},
		})
	}

	return &HAR{
		Log: HARLog{
			Version: "1.2",
			Creator: HARCreator{
				Name:    "Wormhole",
				Version: "0.1.0",
			},
			Entries: harEntries,
		},
	}
}

func statusText(code int) string {
	switch code {
	case 200:
		return "OK"
	case 201:
		return "Created"
	case 204:
		return "No Content"
	case 301:
		return "Moved Permanently"
	case 302:
		return "Found"
	case 304:
		return "Not Modified"
	case 400:
		return "Bad Request"
	case 401:
		return "Unauthorized"
	case 403:
		return "Forbidden"
	case 404:
		return "Not Found"
	case 405:
		return "Method Not Allowed"
	case 500:
		return "Internal Server Error"
	case 502:
		return "Bad Gateway"
	case 503:
		return "Service Unavailable"
	default:
		return ""
	}
}
