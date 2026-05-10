//revive:disable:exported package-comments
package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"time"
)

type CrashReporter struct {
	Dir string
}

type CrashReport struct {
	Path string
	Err  error
}

func DefaultCrashDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".local", "state", "astrotui", "crashes")
	}
	return filepath.Join(home, ".local", "state", "astrotui", "crashes")
}

func (c CrashReporter) Recover(target func(CrashReport)) {
	if recovered := recover(); recovered != nil {
		path, err := c.Write(recovered, debug.Stack())
		if target != nil {
			target(CrashReport{Path: path, Err: err})
		}
	}
}

func (c CrashReporter) Write(recovered any, stack []byte) (string, error) {
	dir := c.Dir
	if dir == "" {
		dir = DefaultCrashDir()
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	path := filepath.Join(dir, time.Now().UTC().Format("20060102T150405.000000000Z")+".log")
	body := fmt.Sprintf("panic: %v\n\n%s", recovered, stack)
	return path, os.WriteFile(path, []byte(body), 0o600)
}

func Safe(reporter CrashReporter, fn func()) (panicked bool) {
	return SafeWithReport(reporter, fn, nil)
}

func SafeWithReport(reporter CrashReporter, fn func(), target func(CrashReport)) (panicked bool) {
	defer reporter.Recover(func(report CrashReport) {
		panicked = true
		if target != nil {
			target(report)
		}
	})
	fn()
	return false
}
