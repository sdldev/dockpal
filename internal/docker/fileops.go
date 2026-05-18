package docker

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/moby/moby/client"
)

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
	execOpts := client.ExecCreateOptions{
		Cmd:          cmd,
		AttachStdout: true,
		AttachStderr: true,
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

	var buf bytes.Buffer
	io.Copy(&buf, resp.Reader)
	return buf.String(), nil
}

func (c *Client) ListFiles(ctx context.Context, containerID, path string) ([]FileInfo, error) {
	if path == "" {
		path = "/"
	}
	cleanPath, err := ValidatePath(path)
	if err != nil {
		return nil, err
	}
	output, err := execCommand(ctx, c.cli, containerID, []string{"sh", "-c", fmt.Sprintf("ls -la '%s'", cleanPath)})
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
	cleanPath, err := ValidatePath(path)
	if err != nil {
		return "", err
	}
	return execCommand(ctx, c.cli, containerID, []string{"cat", cleanPath})
}

func (c *Client) WriteFile(ctx context.Context, containerID, path, content string) error {
	cleanPath, err := ValidatePath(path)
	if err != nil {
		return err
	}

	// Base64 encode content to avoid shell escaping issues
	encoded := base64.StdEncoding.EncodeToString([]byte(content))
	cmd := []string{"sh", "-c", fmt.Sprintf("echo '%s' | base64 -d > '%s'", encoded, cleanPath)}

	output, err := execCommand(ctx, c.cli, containerID, cmd)
	if err != nil {
		return fmt.Errorf("file write failed: %w (output: %s)", err, output)
	}
	return nil
}

func (c *Client) DeleteFile(ctx context.Context, containerID, path string) error {
	cleanPath, err := ValidatePath(path)
	if err != nil {
		return err
	}
	_, err = execCommand(ctx, c.cli, containerID, []string{"rm", "-rf", cleanPath})
	return err
}
