package main

import "errors"

func launchDesktop(string) error {
	return errors.New("the dashboard is macOS-only; use --transcribe FILE_OR_URL, --record, --list, or --export SESSION_ID on Linux (see --help)")
}
func notifyExisting() {}
