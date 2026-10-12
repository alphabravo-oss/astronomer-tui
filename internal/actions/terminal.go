package actions

import (
	"context"
	"os"
	"time"

	"golang.org/x/term"
	"k8s.io/client-go/tools/remotecommand"
)

type terminalSizes struct {
	ctx   context.Context
	sizes <-chan remotecommand.TerminalSize
}

func (q terminalSizes) Next() *remotecommand.TerminalSize {
	select {
	case size := <-q.sizes:
		return &size
	case <-q.ctx.Done():
		return nil
	}
}
func resizeQueue(ctx context.Context, f *os.File) remotecommand.TerminalSizeQueue {
	sizes := make(chan remotecommand.TerminalSize, 1)
	fd := int(f.Fd())
	go func() {
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		var old remotecommand.TerminalSize
		for {
			width, height, err := term.GetSize(fd)
			if err == nil && width > 0 && height > 0 {
				size := remotecommand.TerminalSize{Width: uint16(width), Height: uint16(height)}
				if size != old {
					select {
					case sizes <- size:
						old = size
					default:
					}
				}
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	return terminalSizes{ctx, sizes}
}
