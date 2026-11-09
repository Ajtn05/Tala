//go:build darwin

package dashboard

import (
	"context"
	"fmt"
	"os/exec"
	"tala/internal/audio"
	"tala/internal/media"
	"time"
)

func audioMode(i int) audio.Mode { return audio.Mode(i) }
func contextTimeout() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 2*time.Minute)
}

var probe = media.Probe

func openSignIn(raw, browser string) error {
	if err := media.ValidateURL(raw); err != nil {
		return err
	}
	bundle := map[string]string{
		"chrome": "com.google.Chrome", "chromium": "org.chromium.Chromium",
		"firefox": "org.mozilla.firefox", "safari": "com.apple.Safari",
		"brave": "com.brave.Browser", "edge": "com.microsoft.edgemac",
		"opera": "com.operasoftware.Opera", "vivaldi": "com.vivaldi.Vivaldi",
		"whale": "com.naver.Whale",
		"zen":   "app.zen-browser.zen",
	}[browser]
	args := []string{raw}
	if browser == "dia" {
		args = []string{"-a", "Dia", raw}
	} else if bundle != "" {
		args = []string{"-b", bundle, raw}
	} else if browser != "other" {
		return fmt.Errorf("choose a browser in the sign-in helper first")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := exec.CommandContext(ctx, "/usr/bin/open", args...).Run(); err != nil {
		return fmt.Errorf("could not open %s; make sure it is installed: %w", browser, err)
	}
	return nil
}
