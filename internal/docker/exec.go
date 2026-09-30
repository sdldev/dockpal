package docker

import (
	"context"
	"fmt"
	"sync"

	"github.com/moby/moby/client"
)

// ExecCreate starts an exec instance inside a container and returns its ID.
// The caller attaches to it via ExecAttachAndBridge. The command is a shell
// so the terminal behaves like an interactive session.
func (c *Client) ExecCreate(ctx context.Context, containerID string, cmd []string) (string, error) {
	resp, err := c.cli.ExecCreate(ctx, containerID, client.ExecCreateOptions{
		Cmd:          cmd,
		AttachStdin:  true,
		AttachStdout: true,
		AttachStderr: true,
		TTY:          true, // TTY so shell line-editing and colored output behave
	})
	if err != nil {
		return "", fmt.Errorf("failed to create exec: %w", err)
	}
	if resp.ID == "" {
		return "", fmt.Errorf("exec create returned an empty exec id")
	}
	return resp.ID, nil
}

// TerminalBridge is the transport-agnostic surface the exec bridge pumps
// between the browser WebSocket and the container's TTY. The server wires it
// to a gorilla connection; remote agents can reuse it for their own
// transports.
type TerminalBridge struct {
	// ToContainer delivers a chunk of stdin from the browser.
	ToContainer chan []byte
	// FromContainer delivers a chunk of TTY output toward the browser.
	FromContainer chan []byte
	// Closed is closed once either side terminates the session.
	Closed chan struct{}
	once   sync.Once
}

func NewTerminalBridge() *TerminalBridge {
	return &TerminalBridge{
		ToContainer:   make(chan []byte, 64),
		FromContainer: make(chan []byte, 64),
		Closed:        make(chan struct{}),
	}
}

// Close terminates the session once.
func (b *TerminalBridge) Close() { b.once.Do(func() { close(b.Closed) }) }

// ExecAttachAndBridge attaches to an exec instance (TTY mode) and pumps
// bytes between the attach stream and the bridge until the exec process or
// the browser side ends.
func (c *Client) ExecAttachAndBridge(ctx context.Context, execID string, bridge *TerminalBridge) error {
	attach, err := c.cli.ExecAttach(ctx, execID, client.ExecAttachOptions{
		TTY: true,
	})
	if err != nil {
		return fmt.Errorf("failed to attach exec: %w", err)
	}
	defer attach.Close()

	var wg sync.WaitGroup

	// container TTY → bridge
	wg.Add(1)
	go func() {
		defer wg.Done()
		buf := make([]byte, 8192)
		for {
			n, err := attach.Reader.Read(buf)
			if n > 0 {
				select {
				case bridge.FromContainer <- buf[:n]:
				case <-bridge.Closed:
					return
				}
			}
			if err != nil {
				bridge.Close() // exec process exited (EOF) or stream broke
				return
			}
		}
	}()

	// bridge → container stdin
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case payload, ok := <-bridge.ToContainer:
				if !ok {
					return
				}
				if _, err := attach.Conn.Write(payload); err != nil {
					bridge.Close()
					return
				}
			case <-bridge.Closed:
				return
			case <-ctx.Done():
				return
			}
		}
	}()

	wg.Wait()
	return nil
}
