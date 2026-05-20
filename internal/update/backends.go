package update

import (
	"context"
	"net"
	"os"
	"os/exec"
)

// fsBackend abstracts filesystem operations for testability.
type fsBackend interface {
	Stat(name string) (os.FileInfo, error)
	Rename(oldpath, newpath string) error
	Remove(name string) error
	Chmod(name string, mode os.FileMode) error
	Chown(name string, uid, gid int) error
	WriteFile(name string, data []byte, perm os.FileMode) error
	ReadFile(name string) ([]byte, error)
	MkdirAll(path string, perm os.FileMode) error
	Create(name string) (*os.File, error)
	Sync(f *os.File) error
}

// osFS is the production fsBackend using the real OS.
type osFS struct{}

func (o *osFS) Stat(name string) (os.FileInfo, error)         { return os.Stat(name) }
func (o *osFS) Rename(oldpath, newpath string) error         { return os.Rename(oldpath, newpath) }
func (o *osFS) Remove(name string) error                      { return os.Remove(name) }
func (o *osFS) Chmod(name string, mode os.FileMode) error    { return os.Chmod(name, mode) }
func (o *osFS) Chown(name string, uid, gid int) error        { return os.Chown(name, uid, gid) }
func (o *osFS) WriteFile(name string, data []byte, perm os.FileMode) error {
	return os.WriteFile(name, data, perm)
}
func (o *osFS) ReadFile(name string) ([]byte, error)         { return os.ReadFile(name) }
func (o *osFS) MkdirAll(path string, perm os.FileMode) error { return os.MkdirAll(path, perm) }
func (o *osFS) Create(name string) (*os.File, error)         { return os.Create(name) }
func (o *osFS) Sync(f *os.File) error                       { return f.Sync() }

// serviceController abstracts systemd service control for testability.
type serviceController interface {
	Stop(ctx context.Context) error
	Start(ctx context.Context) error
}

// systemctlController is the production serviceController.
type systemctlController struct{}

func (c *systemctlController) Stop(ctx context.Context) error {
	// Stop is a no-op in production because stopping the service from within
	// itself would kill the updater process before it replaces the binary.
	// The binary swap is fully allowed on Linux while running, and the service
	// reload/restart is initiated in the Start phase.
	return nil
}

func (c *systemctlController) Start(ctx context.Context) error {
	// Asynchronously trigger service restart with --no-block.
	// This schedules the restart with systemd immediately and exits.
	// Dockpal can finish reporting the completion event before systemd halts and restarts it.
	cmd := exec.CommandContext(ctx, "sudo", "systemctl", "restart", "dockpal", "--no-block")
	if output, err := cmd.CombinedOutput(); err != nil {
		return execError(err, output)
	}
	return nil
}

func execError(err error, output []byte) error {
	if len(output) > 0 {
		return &execErrorImpl{err: err, output: string(output)}
	}
	return err
}

type execErrorImpl struct {
	err    error
	output string
}

func (e *execErrorImpl) Error() string {
	if e.output != "" {
		return e.output
	}
	return e.err.Error()
}

func (e *execErrorImpl) Unwrap() error { return e.err }

// sudoChecker abstracts sudo privilege checks for testability.
type sudoChecker interface {
	Check() (bool, error)
}

// sudoCheckerImpl is the production sudoChecker.
type sudoCheckerImpl struct{}

func (s *sudoCheckerImpl) Check() (bool, error) {
	cmd := exec.Command("sudo", "-n", "true")
	if err := cmd.Run(); err != nil {
		return false, nil
	}
	return true, nil
}

// resolver abstracts DNS resolution for testability.
type resolver interface {
	LookupIP(host string) ([]net.IP, error)
}

// netResolver is the production resolver.
type netResolver struct{}

func (n *netResolver) LookupIP(host string) ([]net.IP, error) {
	return net.LookupIP(host)
}
