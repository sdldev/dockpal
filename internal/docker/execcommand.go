package docker

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"time"

	"github.com/moby/moby/client"
)

// One-shot non-interactive exec results shared by the local client and the
// remote-agent HTTP API (the agent mirrors these field names in its JSON).
type ExecCommandResult struct {
	ExitCode   int    `json:"exit_code"`
	Stdout     string `json:"stdout"`
	Stderr     string `json:"stderr"`
	TimedOut   bool   `json:"timed_out,omitempty"`
	Truncated  bool   `json:"truncated,omitempty"`
	DurationMS int64  `json:"duration_ms"`
}

// ExecRequest describes a one-shot non-interactive command inside a container.
type ExecRequest struct {
	Cmd     []string `json:"cmd"`
	Timeout int      `json:"timeout,omitempty"` // seconds, 1..120 (default 30)
	User    string   `json:"user,omitempty"`
}

// Limits bound a one-shot exec run on the local Docker daemon; the agent
// enforces the same caps so every transport behaves identically.
const (
	ExecDefaultTimeout = 30 * time.Second
	ExecMaxTimeout     = 120 * time.Second
	// ExecMaxOutput caps captured stdout+stderr combined at 512 KiB.
	ExecMaxOutput = 512 * 1024
)

// ExecCommand runs a non-interactive command inside a container through the
// local Docker daemon and waits for it to finish, demultiplexing stdout and
// stderr. No TTY and no stdin: the command must terminate by itself; this is
// the transport-independent counterpart of the TTY-based ExecAttachAndBridge.
func (c *Client) ExecCommand(ctx context.Context, containerID string, req ExecRequest) (*ExecCommandResult, error) {
	if err := ValidateContainerID(containerID); err != nil {
		return nil, err
	}
	if len(req.Cmd) == 0 {
		return nil, fmt.Errorf("cmd is required")
	}
	timeout := execTimeout(req.Timeout)

	execResp, err := c.cli.ExecCreate(ctx, containerID, client.ExecCreateOptions{
		Cmd:          req.Cmd,
		AttachStdout: true,
		AttachStderr: true,
		User:         req.User,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create exec: %w", err)
	}

	start := time.Now()
	attachResp, err := c.cli.ExecAttach(ctx, execResp.ID, client.ExecAttachOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to attach exec: %w", err)
	}
	defer attachResp.Close()

	// Drain and demultiplex the stdio stream in the background while the
	// poller below waits for the exec process to exit.
	var outBuf, errBuf bytes.Buffer
	truncated := false
	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		demuxExecStream(attachResp.Reader, &outBuf, &errBuf, &truncated)
	}()

	var timedOut bool
	var exitCode int
	waitForExecFinish(ctx, c.cli, execResp.ID, timeout, &timedOut, &exitCode)
	<-readDone

	return &ExecCommandResult{
		ExitCode:   exitCode,
		Stdout:     outBuf.String(),
		Stderr:     errBuf.String(),
		TimedOut:   timedOut,
		Truncated:  truncated,
		DurationMS: time.Since(start).Milliseconds(),
	}, nil
}

// demuxExecStream splits the docker stdio multiplexed stream into stdout and
// stderr buffers, discarding bytes beyond ExecMaxOutput (combined) so a chatty
// command cannot exhaust memory.
func demuxExecStream(r io.Reader, outBuf, errBuf *bytes.Buffer, truncated *bool) {
	header := make([]byte, 8)
	for {
		if _, err := io.ReadFull(r, header); err != nil {
			return // EOF is the normal end of an exec stream
		}
		// stdio frame header: [streamType, 0, 0, 0, size uint32 BE]
		size := uint64(header[4])<<24 | uint64(header[5])<<16 | uint64(header[6])<<8 | uint64(header[7])
		dst := outBuf
		if header[0] == 2 {
			dst = errBuf
		}
		for size > 0 {
			n := size
			if n > 64*1024 {
				n = 64 * 1024
			}
			chunk := make([]byte, n)
			rn, rerr := io.ReadFull(r, chunk)
			if outBuf.Len()+errBuf.Len()+rn <= ExecMaxOutput {
				dst.Write(chunk[:rn])
			} else {
				*truncated = true
			}
			if rerr != nil {
				return
			}
			size -= uint64(rn)
		}
	}
}

func execTimeout(requested int) time.Duration {
	timeout := time.Duration(requested) * time.Second
	if timeout == 0 {
		timeout = ExecDefaultTimeout
	}
	if timeout > ExecMaxTimeout {
		timeout = ExecMaxTimeout
	}
	return timeout
}

// waitForExecFinish polls ExecInspect until the exec finishes or the timeout
// fires; on timeout the result is flagged and reported without blocking the
// caller (the runtime reaps the exec process).
func waitForExecFinish(ctx context.Context, cli *client.Client, execID string, timeout time.Duration, timedOut *bool, exitCode *int) {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	for {
		insp, ierr := cli.ExecInspect(ctx, execID, client.ExecInspectOptions{})
		if ierr != nil || !insp.Running {
			if ierr == nil {
				*exitCode = insp.ExitCode
			}
			return
		}
		select {
		case <-deadline.C:
			*timedOut = true
			*exitCode = -1
			return
		default:
		}
		time.Sleep(200 * time.Millisecond)
	}
}
