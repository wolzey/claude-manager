package runner

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

var ErrWorkerBusy = errors.New("worker busy")

type Lock struct {
	f    *os.File
	path string
}

// Acquire takes an exclusive non-blocking flock on path. Returns ErrWorkerBusy
// if another process already holds it.
func Acquire(path string) (*Lock, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, ErrWorkerBusy
		}
		return nil, fmt.Errorf("flock: %w", err)
	}
	return &Lock{f: f, path: path}, nil
}

func (l *Lock) Release() error {
	if l == nil || l.f == nil {
		return nil
	}
	_ = syscall.Flock(int(l.f.Fd()), syscall.LOCK_UN)
	err := l.f.Close()
	_ = os.Remove(l.path)
	return err
}
