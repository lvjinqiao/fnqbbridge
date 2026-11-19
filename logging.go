package main

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"
)

type rotatingLog struct {
	mu      sync.Mutex
	path    string
	max     int64
	backups int
}

func (w *rotatingLog) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	size := len(p)
	if int64(size) > w.max {
		p = p[:w.max]
	}
	st, e := os.Lstat(w.path)
	if e == nil && st.Mode()&os.ModeSymlink != 0 {
		return 0, fmt.Errorf("日志路径不能为符号链接")
	}
	if e == nil && st.Size() > w.max {
		f, err := os.Open(w.path)
		if err != nil {
			return 0, err
		}
		_, err = f.Seek(-w.max, io.SeekEnd)
		if err != nil {
			f.Close()
			return 0, err
		}
		tail, err := io.ReadAll(io.LimitReader(f, w.max))
		f.Close()
		if err != nil {
			return 0, err
		}
		if err = os.WriteFile(w.path+".tmp", tail, 0600); err != nil {
			return 0, err
		}
		if err = os.Rename(w.path+".tmp", w.path); err != nil {
			return 0, err
		}
	}
	if e == nil && st.Size()+int64(len(p)) > w.max {
		os.Remove(fmt.Sprintf("%s.%d", w.path, w.backups))
		for i := w.backups - 1; i >= 1; i-- {
			os.Rename(fmt.Sprintf("%s.%d", w.path, i), fmt.Sprintf("%s.%d", w.path, i+1))
		}
		if e = os.Rename(w.path, w.path+".1"); e != nil {
			return 0, e
		}
	}
	f, e := os.OpenFile(w.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if e != nil {
		return 0, e
	}
	defer f.Close()
	_, e = f.Write(p)
	if e != nil {
		return 0, e
	}
	return size, nil
}

// Event throttle is bounded and excludes credentials; duplicate errors produce one entry per 10s.
type eventLimiter struct {
	mu   sync.Mutex
	seen map[string]time.Time
}

var limiter = eventLimiter{seen: map[string]time.Time{}}

func allowEvent(s string) bool {
	if !strings.Contains(s, "失败") && !strings.Contains(s, "状态=4") && !strings.Contains(s, "状态=5") {
		return true
	}
	limiter.mu.Lock()
	defer limiter.mu.Unlock()
	now := time.Now()
	key := strings.Split(s, " 耗时=")[0]
	if t, ok := limiter.seen[key]; ok && now.Sub(t) < 10*time.Second {
		return false
	}
	if len(limiter.seen) > 256 {
		clear(limiter.seen)
	}
	limiter.seen[key] = now
	return true
}
