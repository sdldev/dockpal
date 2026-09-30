package agent

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
)

// wsDial abstracts WebSocket dialing so both direct.go and direct_stacks.go
// share the same gorilla/websocket setup.
func wsDial(ctx context.Context, wsURL string, httpClient *http.Client, authToken string) (*websocket.Conn, error) {
	dialer := &websocket.Dialer{
		HandshakeTimeout:  15 * time.Second,
		TLSClientConfig:   &tls.Config{InsecureSkipVerify: true},
		EnableCompression: false,
	}

	headers := http.Header{}
	if authToken != "" {
		headers.Set("Authorization", "Bearer "+authToken)
	}

	conn, resp, err := dialer.DialContext(ctx, wsURL, headers)
	if err != nil {
		if resp != nil {
			return nil, fmt.Errorf("websocket dial failed (status %d): %w", resp.StatusCode, err)
		}
		return nil, fmt.Errorf("websocket dial failed: %w", err)
	}

	return conn, nil
}

// wsWriteText writes a text message over the WebSocket connection.
func wsWriteText(conn *websocket.Conn, data []byte) error {
	return conn.WriteMessage(websocket.TextMessage, data)
}

// wsWriteBinary writes a binary message over the WebSocket connection.
func wsWriteBinary(conn *websocket.Conn, data []byte) error {
	return conn.WriteMessage(websocket.BinaryMessage, data)
}

// wsReadMessage reads a message from the WebSocket connection.
// Returns the message payload and any error.
func wsReadMessage(conn *websocket.Conn) ([]byte, error) {
	_, msg, err := conn.ReadMessage()
	return msg, err
}

// wsClose gracefully closes a WebSocket connection.
func wsClose(conn *websocket.Conn) {
	_ = conn.WriteMessage(websocket.CloseMessage,
		websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
	conn.Close()
}
