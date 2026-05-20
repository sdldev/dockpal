package ssh

import (
	"fmt"
	"io"
	"net"
	"os"
	"time"

	"golang.org/x/crypto/ssh"
)

// InstallParams contains all parameters required for installing the agent.
type InstallParams struct {
	Host          string
	Port          int
	User          string
	AuthType      string // "password" or "key"
	AuthSecret    string // Plaintext password or private key content
	InstallDocker bool
	Mode          string // "direct" or "edge"
	Token         string
	ServerHost    string
	AgentImage    string // default "ghcr.io/sdldev/dockpal-agent:latest"
	IsSecureWS    bool   // whether to use wss:// instead of ws://
}

// InstallAgent connects to a remote host via SSH, configures Docker if needed,
// and starts the Dockpal Agent Docker container. All stdout/stderr is written to the provided writer.
func InstallAgent(params InstallParams, w io.Writer) error {
	if params.Port == 0 {
		params.Port = 22
	}
	if params.User == "" {
		params.User = "root"
	}
	if params.AgentImage == "" {
		params.AgentImage = os.Getenv("DOCKPAL_AGENT_IMAGE")
		if params.AgentImage == "" {
			params.AgentImage = "ghcr.io/sdldev/dockpal-agent:latest"
		}
	}

	fmt.Fprintf(w, "[Dockpal Installer] Starting installation on %s:%d as user %s...\n", params.Host, params.Port, params.User)

	// Configure SSH Auth
	var authMethod ssh.AuthMethod
	if params.AuthType == "key" {
		signer, err := ssh.ParsePrivateKey([]byte(params.AuthSecret))
		if err != nil {
			return fmt.Errorf("failed to parse SSH private key: %w", err)
		}
		authMethod = ssh.PublicKeys(signer)
		fmt.Fprintln(w, "[Dockpal Installer] Using SSH private key authentication.")
	} else {
		authMethod = ssh.Password(params.AuthSecret)
		fmt.Fprintln(w, "[Dockpal Installer] Using SSH password authentication.")
	}

	config := &ssh.ClientConfig{
		User: params.User,
		Auth: []ssh.AuthMethod{authMethod},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), // For VPS setups, we bypass strict host verification
		Timeout:         15 * time.Second,
	}

	addr := net.JoinHostPort(params.Host, fmt.Sprintf("%d", params.Port))
	fmt.Fprintf(w, "[Dockpal Installer] Connecting to %s...\n", addr)
	client, err := ssh.Dial("tcp", addr, config)
	if err != nil {
		return fmt.Errorf("failed to connect via SSH: %w", err)
	}
	defer client.Close()

	fmt.Fprintln(w, "[Dockpal Installer] SSH connection established successfully.")

	// 1. Log remote server hostname
	if err := runCommand(client, "hostname", w); err != nil {
		return fmt.Errorf("failed to retrieve hostname: %w", err)
	}

	// 2. Check if Docker is installed
	fmt.Fprintln(w, "[Dockpal Installer] Checking if Docker is installed on target host...")
	dockerInstalled := true
	if err := runCommand(client, "command -v docker", io.Discard); err != nil {
		dockerInstalled = false
	}

	if !dockerInstalled {
		if !params.InstallDocker {
			fmt.Fprintln(w, "[Dockpal Installer] Error: Docker is not installed on the remote host, and InstallDocker is disabled.")
			return fmt.Errorf("docker is not installed on remote host")
		}

		fmt.Fprintln(w, "[Dockpal Installer] Docker not found. Installing Docker using the official script...")
		// Use curl to install docker
		installDockerCmd := "curl -fsSL https://get.docker.com | sh"
		if params.User != "root" {
			installDockerCmd = "sudo " + installDockerCmd
		}
		if err := runCommand(client, installDockerCmd, w); err != nil {
			return fmt.Errorf("failed to install Docker: %w", err)
		}

		// Enable and start Docker service
		enableDockerCmd := "systemctl enable --now docker"
		if params.User != "root" {
			enableDockerCmd = "sudo " + enableDockerCmd
		}
		_ = runCommand(client, enableDockerCmd, io.Discard) // ignore error if systemctl is not available
		fmt.Fprintln(w, "[Dockpal Installer] Docker installation finished.")
	} else {
		fmt.Fprintln(w, "[Dockpal Installer] Docker is already installed.")
	}

	// 3. Ensure Docker daemon is accessible
	fmt.Fprintln(w, "[Dockpal Installer] Verifying Docker daemon accessibility...")
	dockerInfoCmd := "docker info"
	if params.User != "root" {
		dockerInfoCmd = "sudo " + dockerInfoCmd
	}
	if err := runCommand(client, dockerInfoCmd, io.Discard); err != nil {
		// If normal user doesn't have permissions, try running with sudo, or add to docker group
		if params.User != "root" {
			fmt.Fprintln(w, "[Dockpal Installer] Current user might not have docker permissions. Attempting to add user to docker group...")
			_ = runCommand(client, fmt.Sprintf("sudo usermod -aG docker %s", params.User), io.Discard)
		} else {
			return fmt.Errorf("docker daemon is not accessible: %w", err)
		}
	}

	// 4. Pull dockpal-agent image
	fmt.Fprintf(w, "[Dockpal Installer] Pulling Agent Docker image: %s...\n", params.AgentImage)
	pullCmd := fmt.Sprintf("docker pull %s", params.AgentImage)
	if params.User != "root" {
		pullCmd = "sudo " + pullCmd
	}
	if err := runCommand(client, pullCmd, w); err != nil {
		return fmt.Errorf("failed to pull agent image: %w", err)
	}

	// 5. Remove existing agent container if any
	fmt.Fprintln(w, "[Dockpal Installer] Cleaning up any existing dockpal-agent container...")
	rmCmd := "docker rm -f dockpal-agent"
	if params.User != "root" {
		rmCmd = "sudo " + rmCmd
	}
	_ = runCommand(client, rmCmd, io.Discard)

	// 6. Build docker run command
	var runCmd string
	if params.Mode == "direct" {
		runCmd = fmt.Sprintf(
			"docker run -d --name dockpal-agent --restart unless-stopped -v /var/run/docker.sock:/var/run/docker.sock -p 9273:9273 -e DOCKPAL_MODE=direct -e DOCKPAL_TOKEN=%s %s",
			params.Token,
			params.AgentImage,
		)
	} else {
		// Edge mode
		scheme := "ws"
		if params.IsSecureWS {
			scheme = "wss"
		}
		wsURL := fmt.Sprintf("%s://%s/api/agent/connect", scheme, params.ServerHost)
		// We set both DOCKPAL_EDGE_SERVER and DOCKPAL_SERVER to prevent mismatch issues
		runCmd = fmt.Sprintf(
			"docker run -d --name dockpal-agent --restart unless-stopped -v /var/run/docker.sock:/var/run/docker.sock -e DOCKPAL_MODE=edge -e DOCKPAL_EDGE_SERVER=%s -e DOCKPAL_SERVER=%s -e DOCKPAL_TOKEN=%s %s",
			wsURL,
			wsURL,
			params.Token,
			params.AgentImage,
		)
	}

	if params.User != "root" {
		runCmd = "sudo " + runCmd
	}

	fmt.Fprintln(w, "[Dockpal Installer] Starting Dockpal Agent container...")
	if err := runCommand(client, runCmd, w); err != nil {
		return fmt.Errorf("failed to run agent container: %w", err)
	}

	fmt.Fprintln(w, "[Dockpal Installer] Dockpal Agent container started successfully!")
	return nil
}

// runCommand runs a command on an SSH client and redirects output to w.
func runCommand(client *ssh.Client, cmd string, w io.Writer) error {
	session, err := client.NewSession()
	if err != nil {
		return err
	}
	defer session.Close()

	session.Stdout = w
	session.Stderr = w

	return session.Run(cmd)
}
