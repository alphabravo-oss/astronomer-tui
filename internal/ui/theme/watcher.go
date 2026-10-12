//revive:disable:exported package-comments
package theme

import (
	"context"
	"os"
	"time"
)

type Update struct {
	Skin Skin
	Err  error
}

type Watcher struct {
	Path     string
	Fallback string
	Interval time.Duration
}

func (w Watcher) Watch(ctx context.Context) <-chan Update {
	ch := make(chan Update, 1)
	interval := w.Interval
	if interval <= 0 {
		interval = time.Second
	}
	go func() {
		defer close(ch)
		var lastMod time.Time
		var lastSize int64
		var existed bool
		pending := false
		emit := func() {
			skin, err := Load(w.Path, w.Fallback)
			select {
			case ch <- Update{Skin: skin, Err: err}:
			case <-ctx.Done():
			}
		}
		emit()
		if info, err := os.Stat(w.Path); err == nil {
			lastMod, lastSize, existed = info.ModTime(), info.Size(), true
		}
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				info, err := os.Stat(w.Path)
				exists := err == nil
				var mod time.Time
				var size int64
				if exists {
					mod, size = info.ModTime(), info.Size()
				}
				if exists != existed || !mod.Equal(lastMod) || size != lastSize {
					// Editors may truncate and rewrite in separate operations. Require a
					// stable observation before loading, including deletion/recreation.
					lastMod, lastSize, existed = mod, size, exists
					pending = true
					continue
				}
				if pending {
					pending = false
					emit()
				}

			}
		}
	}()
	return ch
}
