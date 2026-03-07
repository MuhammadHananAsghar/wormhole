package transport

import "context"

// Message represents a message sent/received over the transport.
type Message struct {
	Data []byte
}

// Transport is the interface for client-relay communication.
type Transport interface {
	// Connect establishes a connection to the relay.
	Connect(ctx context.Context) error
	// Send sends a message to the relay.
	Send(ctx context.Context, msg Message) error
	// Receive blocks until a message is received or context is cancelled.
	Receive(ctx context.Context) (Message, error)
	// Close cleanly shuts down the transport.
	Close() error
}
