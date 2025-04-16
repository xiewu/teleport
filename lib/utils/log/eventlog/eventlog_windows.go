package eventlog

import (
	"github.com/gravitational/trace"
	"golang.org/x/sys/windows/svc/eventlog"
)

func Install(source string) error {
	return trace.Wrap(
		eventlog.InstallAsEventCreate(source, eventlog.Info|eventlog.Warning|eventlog.Error),
	)
}

func Remove(source string) error {
	return trace.Wrap(eventlog.Remove(source))
}
