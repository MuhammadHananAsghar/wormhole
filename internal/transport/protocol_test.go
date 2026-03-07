package transport

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseMessageType(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
		wantErr  bool
	}{
		{"register", `{"type":"register","protocol":"http"}`, TypeRegister, false},
		{"registered", `{"type":"registered","subdomain":"abc123","url":"https://abc123.wormhole.bar"}`, TypeRegistered, false},
		{"http_request", `{"type":"http_request","id":"req_1"}`, TypeHTTPRequest, false},
		{"http_response", `{"type":"http_response","id":"req_1","status":200}`, TypeHTTPResponse, false},
		{"invalid json", `not json`, "", true},
		{"empty type", `{"type":""}`, "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseMessageType([]byte(tt.input))
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.expected, got)
			}
		})
	}
}

func TestRegisterMessageRoundTrip(t *testing.T) {
	msg := RegisterMessage{Type: TypeRegister, Protocol: "http"}
	data, err := json.Marshal(msg)
	require.NoError(t, err)

	var decoded RegisterMessage
	require.NoError(t, json.Unmarshal(data, &decoded))
	assert.Equal(t, msg, decoded)
}

func TestHTTPRequestMessageRoundTrip(t *testing.T) {
	body := "aGVsbG8=" // base64 "hello"
	msg := HTTPRequestMessage{
		Type:    TypeHTTPRequest,
		ID:      "req_1",
		Method:  "POST",
		Path:    "/api/test",
		Headers: map[string]string{"Content-Type": "application/json"},
		Body:    &body,
	}
	data, err := json.Marshal(msg)
	require.NoError(t, err)

	var decoded HTTPRequestMessage
	require.NoError(t, json.Unmarshal(data, &decoded))
	assert.Equal(t, msg.ID, decoded.ID)
	assert.Equal(t, msg.Method, decoded.Method)
	assert.Equal(t, *msg.Body, *decoded.Body)
}

func TestRegisterMessageWithSubdomain(t *testing.T) {
	msg := RegisterMessage{Type: TypeRegister, Protocol: "http", Subdomain: "myapp"}
	data, err := json.Marshal(msg)
	require.NoError(t, err)

	var decoded RegisterMessage
	require.NoError(t, json.Unmarshal(data, &decoded))
	assert.Equal(t, "myapp", decoded.Subdomain)
}

func TestRegisterMessageWithToken(t *testing.T) {
	msg := RegisterMessage{Type: TypeRegister, Protocol: "http", Subdomain: "myapp", Token: "abc123"}
	data, err := json.Marshal(msg)
	require.NoError(t, err)

	var decoded RegisterMessage
	require.NoError(t, json.Unmarshal(data, &decoded))
	assert.Equal(t, "abc123", decoded.Token)
}

func TestRegisterMessageOmitsEmptyFields(t *testing.T) {
	msg := RegisterMessage{Type: TypeRegister, Protocol: "http"}
	data, err := json.Marshal(msg)
	require.NoError(t, err)

	assert.NotContains(t, string(data), "subdomain")
	assert.NotContains(t, string(data), "token")
}

func TestRegisterErrorMessageRoundTrip(t *testing.T) {
	msg := RegisterErrorMessage{Type: TypeRegisterError, Error: "subdomain taken"}
	data, err := json.Marshal(msg)
	require.NoError(t, err)

	var decoded RegisterErrorMessage
	require.NoError(t, json.Unmarshal(data, &decoded))
	assert.Equal(t, TypeRegisterError, decoded.Type)
	assert.Equal(t, "subdomain taken", decoded.Error)
}

func TestWSOpenMessageRoundTrip(t *testing.T) {
	msg := WSOpenMessage{
		Type:    TypeWSOpen,
		ID:      "ws_1",
		Path:    "/ws",
		Headers: map[string]string{"Origin": "http://localhost:3000"},
	}
	data, err := json.Marshal(msg)
	require.NoError(t, err)

	var decoded WSOpenMessage
	require.NoError(t, json.Unmarshal(data, &decoded))
	assert.Equal(t, msg.ID, decoded.ID)
	assert.Equal(t, msg.Path, decoded.Path)
	assert.Equal(t, "http://localhost:3000", decoded.Headers["Origin"])
}

func TestWSFrameMessageRoundTrip(t *testing.T) {
	msg := WSFrameMessage{Type: TypeWSFrame, ID: "ws_1", Data: "aGVsbG8="}
	data, err := json.Marshal(msg)
	require.NoError(t, err)

	var decoded WSFrameMessage
	require.NoError(t, json.Unmarshal(data, &decoded))
	assert.Equal(t, msg.ID, decoded.ID)
	assert.Equal(t, "aGVsbG8=", decoded.Data)
}

func TestWSCloseMessageRoundTrip(t *testing.T) {
	msg := WSCloseMessage{Type: TypeWSClose, ID: "ws_1", Code: 1000}
	data, err := json.Marshal(msg)
	require.NoError(t, err)

	var decoded WSCloseMessage
	require.NoError(t, json.Unmarshal(data, &decoded))
	assert.Equal(t, msg.ID, decoded.ID)
	assert.Equal(t, 1000, decoded.Code)
}

func TestParseMessageType_NewTypes(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"register_error", `{"type":"register_error","error":"taken"}`, TypeRegisterError},
		{"ws_open", `{"type":"ws_open","id":"ws_1"}`, TypeWSOpen},
		{"ws_frame", `{"type":"ws_frame","id":"ws_1","data":"abc"}`, TypeWSFrame},
		{"ws_close", `{"type":"ws_close","id":"ws_1","code":1000}`, TypeWSClose},
		{"ping", `{"type":"ping"}`, TypePing},
		{"pong", `{"type":"pong"}`, TypePong},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseMessageType([]byte(tt.input))
			require.NoError(t, err)
			assert.Equal(t, tt.expected, got)
		})
	}
}

func TestHTTPRequestMessageNilBody(t *testing.T) {
	msg := HTTPRequestMessage{
		Type:   TypeHTTPRequest,
		ID:     "req_2",
		Method: "GET",
		Path:   "/",
		Body:   nil,
	}
	data, err := json.Marshal(msg)
	require.NoError(t, err)

	var decoded HTTPRequestMessage
	require.NoError(t, json.Unmarshal(data, &decoded))
	assert.Nil(t, decoded.Body)
}
