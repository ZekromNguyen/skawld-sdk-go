package tools

import (
	"path/filepath"
	"sync"
)

type FileReadTracker struct {
	mu    sync.RWMutex
	reads map[string]struct{}
}

func NewFileReadTracker() *FileReadTracker {
	return &FileReadTracker{reads: map[string]struct{}{}}
}

func (t *FileReadTracker) MarkRead(absPath string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.reads[clean(absPath)] = struct{}{}
}

func (t *FileReadTracker) HasRead(absPath string) bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	_, ok := t.reads[clean(absPath)]
	return ok
}

func clean(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return filepath.Clean(path)
	}
	return filepath.Clean(abs)
}
