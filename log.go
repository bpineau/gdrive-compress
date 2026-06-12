package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"sync"
	"time"
)

type logEntry struct {
	Time     string `json:"t"`
	FileID   string `json:"id"`
	Name     string `json:"name"`
	OrigSize int64  `json:"orig"`
	NewSize  int    `json:"new,omitempty"`
	Strategy string `json:"strategy,omitempty"`
	Skipped  string `json:"skipped,omitempty"`
	Error    string `json:"error,omitempty"`
	DryRun   bool   `json:"dry,omitempty"`
}

type progressLog struct {
	mu sync.Mutex
	f  *os.File
}

func openLog(path string) (*progressLog, error) {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, err
	}
	return &progressLog{f: f}, nil
}

func (l *progressLog) Close() error {
	if l == nil || l.f == nil {
		return nil
	}
	return l.f.Close()
}

func (l *progressLog) write(e logEntry) error {
	e.Time = time.Now().UTC().Format(time.RFC3339)
	l.mu.Lock()
	defer l.mu.Unlock()
	enc := json.NewEncoder(l.f)
	return enc.Encode(e)
}

// loadProcessed returns the set of file IDs that previously completed
// successfully (no Error, not skipped, not dry-run). Errors and dry-run
// rows are kept out so re-runs retry them.
func loadProcessed(path string) (map[string]bool, error) {
	out := map[string]bool{}
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return out, nil
		}
		return nil, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		var e logEntry
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			continue
		}
		if e.Error != "" || e.DryRun {
			continue
		}
		if e.FileID == "" {
			continue
		}
		out[e.FileID] = true
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("scan log: %w", err)
	}
	return out, nil
}
