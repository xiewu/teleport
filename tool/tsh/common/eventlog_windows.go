package common

import (
	"context"
	"os"
	"path/filepath"

	"github.com/gravitational/trace"
	"golang.org/x/sys/windows/svc/eventlog"
)

const eventSource = "tsh"

func testEventLog() error {
	// TODO: Copy what the eventlog package is doing and create a separate log for tsh or VNet.
	log, err := eventlog.Open(eventSource)
	if err != nil {
		return trace.Wrap(err)
	}

	if err := log.Info(10000, "Hello, World!"); err != nil {
		return trace.Wrap(err)
	}
	return nil

}

func installEventLog() error {
	exe, err := os.Executable()
	if err != nil {
		return trace.Wrap(err)
	}
	msgFile := filepath.Join(exe, "..", "..", "msgfile.dll")
	logger.DebugContext(context.Background(), "Calculated msgFile", "path", msgFile)

	return trace.Wrap(
		eventlog.Install(eventSource, msgFile, false /* useExpandKey */, eventlog.Info|eventlog.Warning|eventlog.Error),
	)
}

func uninstallEventLog() error {
	return trace.Wrap(eventlog.Remove(eventSource))
}
