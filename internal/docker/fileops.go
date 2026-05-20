package docker

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/moby/moby/client"
)

// containerIDRegex validates Docker container IDs (hex, 12-64 chars) and names.
var containerIDRegex = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.\-]{0,63}$`)

// ValidateContainerID ensures a container ID/name is safe for exec operations.
func ValidateContainerID(id string) error {
	if id == "" {
		return fmt.Errorf("container ID is required")
	}
	if !containerIDRegex.MatchString(id) {
		return fmt.Errorf("invalid container ID format")
	}
	return nil
}

// ValidatePath ensures the path is safe for container file operations.
// Returns the cleaned path or an error if the path is invalid.
func ValidatePath(path string) (string, error) {
	// Reject null bytes and control characters
	for _, c := range path {
		if c == 0 || (c < 32 && c != '\t') {
			return "", fmt.Errorf("path contains invalid control character")
		}
	}

	cleaned := filepath.Clean(path)

	// Must be absolute
	if !strings.HasPrefix(cleaned, "/") {
		return "", fmt.Errorf("path must be absolute")
	}

	// After cleaning, no ".." should remain that escapes root
	if strings.Contains(cleaned, "..") {
		return "", fmt.Errorf("path traversal not allowed")
	}

	return cleaned, nil
}

type FileInfo struct {
	Name  string `json:"name"`
	Size  string `json:"size"`
	IsDir bool   `json:"is_dir"`
}

func execCommand(ctx context.Context, cli *client.Client, containerID string, cmd []string) (string, error) {
	return execCommandWithInput(ctx, cli, containerID, cmd, nil)
}

func execCommandWithInput(ctx context.Context, cli *client.Client, containerID string, cmd []string, input io.Reader) (string, error) {
	execOpts := client.ExecCreateOptions{
		Cmd:          cmd,
		AttachStdout: true,
		AttachStderr: true,
		AttachStdin:  input != nil,
	}

	result, err := cli.ExecCreate(ctx, containerID, execOpts)
	if err != nil {
		return "", fmt.Errorf("exec create: %w", err)
	}

	attachOpts := client.ExecAttachOptions{}
	resp, err := cli.ExecAttach(ctx, result.ID, attachOpts)
	if err != nil {
		return "", fmt.Errorf("exec attach: %w", err)
	}
	defer resp.Close()

	if input != nil {
		go func() {
			_, _ = io.Copy(resp.Conn, input)
			_ = resp.CloseWrite()
		}()
	}

	var buf bytes.Buffer
	io.Copy(&buf, resp.Reader)
	return buf.String(), nil
}

func (c *Client) ListFiles(ctx context.Context, containerID, path string) ([]FileInfo, error) {
	if err := ValidateContainerID(containerID); err != nil {
		return nil, err
	}
	if path == "" {
		path = "/"
	}
	cleanPath, err := ValidatePath(path)
	if err != nil {
		return nil, err
	}
	output, err := execCommand(ctx, c.cli, containerID, []string{"ls", "-la", cleanPath})
	if err != nil {
		return nil, err
	}

	lines := bytes.Split([]byte(output), []byte("\n"))
	var files []FileInfo
	for i, line := range lines {
		if i == 0 || len(line) == 0 {
			continue
		}
		parts := bytes.Fields(line)
		if len(parts) < 9 {
			continue
		}
		name := string(parts[8])
		if name == "." || name == ".." {
			continue
		}
		isDir := parts[0][0] == 'd'
		files = append(files, FileInfo{
			Name:  name,
			Size:  string(parts[4]),
			IsDir: isDir,
		})
	}
	return files, nil
}

func (c *Client) ReadFile(ctx context.Context, containerID, path string) (string, error) {
	if err := ValidateContainerID(containerID); err != nil {
		return "", err
	}
	cleanPath, err := ValidatePath(path)
	if err != nil {
		return "", err
	}
	return execCommand(ctx, c.cli, containerID, []string{"cat", cleanPath})
}

func (c *Client) WriteFile(ctx context.Context, containerID, path, content string) error {
	if err := ValidateContainerID(containerID); err != nil {
		return err
	}
	cleanPath, err := ValidatePath(path)
	if err != nil {
		return err
	}

	cmd := []string{"tee", cleanPath}

	output, err := execCommandWithInput(ctx, c.cli, containerID, cmd, strings.NewReader(content))
	if err != nil {
		return fmt.Errorf("file write failed: %w (output: %s)", err, output)
	}
	return nil
}

func (c *Client) DeleteFile(ctx context.Context, containerID, path string) error {
	if err := ValidateContainerID(containerID); err != nil {
		return err
	}
	cleanPath, err := ValidatePath(path)
	if err != nil {
		return err
	}
	// Block deletion of critical paths
	blocked := []string{"/", "/etc", "/bin", "/sbin", "/usr", "/var", "/boot", "/dev", "/proc", "/sys", "/lib", "/opt"}
	for _, b := range blocked {
		if cleanPath == b || cleanPath == b+"/" {
			return fmt.Errorf("deleting %s is not allowed for safety", cleanPath)
		}
	}
	_, err = execCommand(ctx, c.cli, containerID, []string{"rm", "-rf", cleanPath})
	return err
}
