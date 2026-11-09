package media

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBrowserSignInAppliedToMetadataAndDownload(t *testing.T) {
	dir := t.TempDir()
	argPath := filepath.Join(dir, "args")
	t.Setenv("TALA_TEST_AUTH_ARGS", argPath)
	t.Setenv("PATH", dir)
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$TALA_TEST_AUTH_ARGS\"\nprintf '%s' '{\"title\":\"Signed-in media\",\"duration\":42}'\n"
	if err := os.WriteFile(filepath.Join(dir, "yt-dlp"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	download := t.TempDir()
	if err := os.WriteFile(filepath.Join(download, "source.m4a"), []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	raw := "https://example.org/video?a=$(touch%20bad)"
	auth := URLAuth{Browser: "chrome", Profile: "Profile 1 $(touch bad)"}
	for _, fetch := range []bool{true, false} {
		var err error
		if fetch {
			_, err = FetchWithAuth(context.Background(), raw, auth)
		} else {
			_, err = DownloadWithAuth(context.Background(), raw, download, auth)
		}
		if err != nil {
			t.Fatal(err)
		}
		b, _ := os.ReadFile(argPath)
		args := string(b)
		if !strings.Contains(args, "--cookies-from-browser\nchrome:Profile 1 $(touch bad)\n") || !strings.Contains(args, "--\n"+raw+"\n") || !strings.Contains(args, "--no-cookies\n") {
			t.Fatalf("browser selection or isolated arguments lost: %s", args)
		}
	}
	if _, err := Fetch(context.Background(), raw); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(argPath)
	if strings.Contains(string(b), "--cookies-from-browser") {
		t.Fatal("public links read browser cookies")
	}
}

func TestBrowserSignInValidationAndErrorGuidance(t *testing.T) {
	for _, auth := range []URLAuth{{Browser: "chrome --exec=bad"}, {Profile: "Default"}, {Browser: "safari", Profile: "Default"}, {Browser: "firefox", Profile: "name::container"}, {Browser: "chrome", Profile: "bad\nprofile"}, {Browser: "dia"}, {Browser: "other"}, {Browser: "chrome", CookiesFile: "/cookies.txt"}, {Browser: "other", CookiesFile: "https://example.org/cookies.txt"}, {Browser: "dia", Profile: "Default", CookiesFile: "/cookies.txt"}} {
		if auth.Validate() == nil {
			t.Errorf("accepted %+v", auth)
		}
	}
	dir := t.TempDir()
	t.Setenv("PATH", dir)
	if err := os.WriteFile(filepath.Join(dir, "yt-dlp"), []byte("#!/bin/sh\nprintf 'Sign in to confirm your age' >&2\nexit 1\n"), 0700); err != nil {
		t.Fatal(err)
	}
	_, err := Fetch(context.Background(), "https://example.org/video")
	if err == nil || !strings.Contains(err.Error(), "URL sign-in helper") || !strings.Contains(err.Error(), "confirm your age") {
		t.Fatalf("original error or recovery guidance lost: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = FetchWithAuth(ctx, "https://example.org/video", URLAuth{Browser: "firefox"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %v", err)
	}
}

func TestZenUsesItsOwnFirefoxCompatibleProfile(t *testing.T) {
	for _, tc := range []struct{ platform, relative string }{
		{"darwin", "Library/Application Support/zen/Profiles"},
		{"linux", ".zen"},
		{"linux", ".var/app/app.zen_browser.zen/.zen"},
	} {
		home := t.TempDir()
		root := filepath.Join(home, tc.relative)
		profile := filepath.Join(root, "fixture.Default (release)")
		if err := os.MkdirAll(profile, 0700); err != nil {
			t.Fatal(err)
		}
		for _, selection := range []struct{ profile, want string }{
			{"", root}, {"fixture.Default (release)", profile}, {profile, profile},
			{"~/" + tc.relative + "/fixture.Default (release)", profile},
		} {
			got, err := (URLAuth{Browser: "zen", Profile: selection.profile}).browserSelector(home, tc.platform)
			if err != nil || got != "firefox:"+selection.want {
				t.Fatalf("Zen selector = %q, %v", got, err)
			}
		}
	}
	if _, err := (URLAuth{Browser: "zen"}).browserSelector(t.TempDir(), "darwin"); err == nil {
		t.Fatal("missing Zen profile silently used Firefox")
	}
}

func TestCookieExportIsPrivateAndOriginalIsPreserved(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "cookie export $(touch bad).txt")
	const contents = "# Netscape HTTP Cookie File\n.example.org\tTRUE\t/\tTRUE\t2147483647\tsession\tfixture-only\n"
	if err := os.WriteFile(source, []byte(contents), 0400); err != nil {
		t.Fatal(err)
	}
	path, cleanup, err := temporaryCookies(source)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("temporary file permissions: %v %v", info, err)
	}
	info, err = os.Stat(filepath.Dir(path))
	if err != nil || info.Mode().Perm() != 0700 {
		t.Fatalf("temporary directory permissions: %v %v", info, err)
	}
	cleanup()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("temporary cookies survived cleanup")
	}

	argPath := filepath.Join(dir, "args")
	t.Setenv("TALA_TEST_AUTH_ARGS", argPath)
	t.Setenv("PATH", dir)
	for _, mode := range []string{"success", "failure", "cancel"} {
		script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$TALA_TEST_AUTH_ARGS\"\nwhile [ \"$1\" != '--cookies' ]; do shift; done\nshift\nprintf 'updated jar' > \"$1\"\n"
		switch mode {
		case "success":
			script += "printf '%s' '{\"title\":\"Cookie fixture\",\"duration\":42}'\n"
		case "failure":
			script += "printf 'site unavailable' >&2\nexit 1\n"
		case "cancel":
			script += "/bin/sleep 10\n"
		}
		if err := os.WriteFile(filepath.Join(dir, "yt-dlp"), []byte(script), 0700); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		_, err := FetchWithAuth(ctx, "https://example.org/media", URLAuth{Browser: "dia", CookiesFile: source})
		cancel()
		if (mode == "success") != (err == nil) {
			t.Fatalf("%s: %v", mode, err)
		}
		if mode == "cancel" && !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("cancellation lost: %v", err)
		}
		b, err := os.ReadFile(argPath)
		if err != nil {
			t.Fatal(err)
		}
		args := strings.Split(string(b), "\n")
		copied := ""
		for i, a := range args {
			if a == "--cookies-from-browser" {
				t.Fatal("cookie-file mode tried a native browser reader")
			}
			if a == "--cookies" {
				copied = args[i+1]
			}
		}
		if copied == "" || copied == source {
			t.Fatal("yt-dlp received the original cookie file")
		}
		if _, err := os.Stat(copied); !os.IsNotExist(err) {
			t.Fatalf("%s left temporary credentials", mode)
		}
		b, _ = os.ReadFile(source)
		if string(b) != contents {
			t.Fatal("original cookies changed")
		}
	}
}

func TestCookieExportValidationDoesNotEchoContents(t *testing.T) {
	for _, contents := range []string{"https://example.org/login", "{\"secret\":\"fixture\"}", "SQLite format 3\x00"} {
		path := filepath.Join(t.TempDir(), "invalid.txt")
		if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
			t.Fatal(err)
		}
		_, _, err := temporaryCookies(path)
		if err == nil || strings.Contains(err.Error(), contents) {
			t.Fatalf("invalid cookie file accepted or echoed: %v", err)
		}
	}
}
