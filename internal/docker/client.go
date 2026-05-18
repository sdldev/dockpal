package docker

import "github.com/moby/moby/client"

// Re-export client for direct use
type DockerClient = client.Client

// RawClient returns the underlying moby Docker client for use by
// modules that need direct Docker SDK access (e.g., tunnel management).
func (c *Client) RawClient() *client.Client {
	return c.cli
}
