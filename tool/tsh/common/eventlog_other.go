//go:build !windows
// +build !windows

package common

func testEventLog() error {
	return nil
}

func installEventLog() error {
	return nil
}

func uninstallEventLog() error {
	return nil
}
