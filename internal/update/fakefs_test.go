package update

import (
	"context"
	"fmt"
	"net"
	"os"
	"sync"
	"time"
)

// memFS is an in-memory filesystem for tests.
type memFS struct {
	mu      sync.Mutex
	files   map[string][]byte
	modes   map[string]os.FileMode
	owners  map[string][2]int // uid, gid
	infos   map[string]fakeFileInfo
}

func newMemFS() *memFS {
	return &memFS{
		files:  make(map[string][]byte),
		modes:  make(map[string]os.FileMode),
		owners: make(map[string][2]int),
		infos:  make(map[string]fakeFileInfo),
	}
}

func (m *memFS) Stat(name string) (os.FileInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if info, ok := m.infos[name]; ok {
		return info, nil
	}
	if data, ok := m.files[name]; ok {
		mode := m.modes[name]
		if mode == 0 {
			mode = 0644
		}
		return fakeFileInfo{name: name, size: int64(len(data)), mode: mode, uid: m.owners[name][0], gid: m.owners[name][1]}, nil
	}
	return nil, os.ErrNotExist
}

func (m *memFS) Rename(oldpath, newpath string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	data, ok := m.files[oldpath]
	if !ok {
		return os.ErrNotExist
	}
	m.files[newpath] = data
	m.modes[newpath] = m.modes[oldpath]
	m.owners[newpath] = m.owners[oldpath]
	m.infos[newpath] = m.infos[oldpath]
	delete(m.files, oldpath)
	delete(m.modes, oldpath)
	delete(m.owners, oldpath)
	delete(m.infos, oldpath)
	return nil
}

func (m *memFS) Remove(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.files, name)
	delete(m.modes, name)
	delete(m.owners, name)
	delete(m.infos, name)
	return nil
}

func (m *memFS) Chmod(name string, mode os.FileMode) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.files[name]; !ok {
		return os.ErrNotExist
	}
	m.modes[name] = mode
	return nil
}

func (m *memFS) Chown(name string, uid, gid int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.files[name]; !ok {
		return os.ErrNotExist
	}
	m.owners[name] = [2]int{uid, gid}
	return nil
}

func (m *memFS) WriteFile(name string, data []byte, perm os.FileMode) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.files[name] = data
	m.modes[name] = perm
	m.owners[name] = [2]int{0, 0}
	return nil
}

func (m *memFS) ReadFile(name string) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	data, ok := m.files[name]
	if !ok {
		return nil, os.ErrNotExist
	}
	return data, nil
}

func (m *memFS) MkdirAll(path string, perm os.FileMode) error { return nil }

func (m *memFS) Create(name string) (*os.File, error) {
	// memFS Create is tricky because we need a real *os.File for Sync.
	// We write to a temp real file and track it.
	f, err := os.CreateTemp("", "memfs-")
	if err != nil {
		return nil, err
	}
	return f, nil
}

func (m *memFS) Sync(f *os.File) error { return f.Sync() }

// fakeFileInfo implements os.FileInfo.
type fakeFileInfo struct {
	name string
	size int64
	mode os.FileMode
	uid  int
	gid  int
}

func (f fakeFileInfo) Name() string       { return f.name }
func (f fakeFileInfo) Size() int64        { return f.size }
func (f fakeFileInfo) Mode() os.FileMode { return f.mode }
func (f fakeFileInfo) ModTime() time.Time { return time.Now() }
func (f fakeFileInfo) IsDir() bool        { return false }
func (f fakeFileInfo) Sys() interface{} { return &syscallStat_t{Uid_: uint32(f.uid), Gid_: uint32(f.gid)} }

type syscallStat_t struct {
	Uid_ uint32
	Gid_ uint32
}

func (s *syscallStat_t) Uid() uint32 { return s.Uid_ }
func (s *syscallStat_t) Gid() uint32 { return s.Gid_ }

// scriptedController returns pre-configured success or error per call index.
type scriptedController struct {
	mu     sync.Mutex
	calls  int
	stops  []error
	starts []error
}

func newScriptedController(stops, starts []error) *scriptedController {
	return &scriptedController{stops: stops, starts: starts}
}

func (c *scriptedController) Stop(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	idx := c.calls
	c.calls++
	if idx < len(c.stops) {
		return c.stops[idx]
	}
	return nil
}

func (c *scriptedController) Start(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	idx := c.calls
	c.calls++
	if idx < len(c.starts) {
		return c.starts[idx]
	}
	return nil
}

// scriptedSudoChecker returns pre-configured sequence of bool results.
type scriptedSudoChecker struct {
	mu      sync.Mutex
	calls   int
	results []bool
}

func newScriptedSudoChecker(results []bool) *scriptedSudoChecker {
	return &scriptedSudoChecker{results: results}
}

func (s *scriptedSudoChecker) Check() (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	idx := s.calls
	s.calls++
	if idx < len(s.results) {
		return s.results[idx], nil
	}
	return false, nil
}

// stubResolver returns configured IPs per host.
type stubResolver struct {
	mu       sync.Mutex
	hostIPs  map[string][]net.IP
	lookuper map[string]error
}

func newStubResolver() *stubResolver {
	return &stubResolver{
		hostIPs:  make(map[string][]net.IP),
		lookuper: make(map[string]error),
	}
}

func (r *stubResolver) AddHost(host string, ips []net.IP) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.hostIPs[host] = ips
}

func (r *stubResolver) AddError(host string, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lookuper[host] = err
}

func (r *stubResolver) LookupIP(host string) ([]net.IP, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err, ok := r.lookuper[host]; ok {
		return nil, err
	}
	if ips, ok := r.hostIPs[host]; ok {
		return ips, nil
	}
	return nil, fmt.Errorf("no entry for host %s", host)
}
