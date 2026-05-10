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
		emit := func() {
			skin, err := Load(w.Path, w.Fallback)
			select {
			case ch <- Update{Skin: skin, Err: err}:
			case <-ctx.Done():
			}
		}
		emit()
		if info, err := os.Stat(w.Path); err == nil {
			lastMod = info.ModTime()
		}
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				info, err := os.Stat(w.Path)
				if err != nil {
					if !lastMod.IsZero() {
						lastMod = time.Time{}
						emit()
					}
					continue
				}
				if info.ModTime().After(lastMod) {
					lastMod = info.ModTime()
					emit()
				}
			}
		}
	}()
	return ch
}
