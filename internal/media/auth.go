package media

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

// URLAuth stores only browser/profile selections and an optional cookie-file path.
type URLAuth struct {
	Browser     string `json:"browser,omitempty"`
	Profile     string `json:"profile,omitempty"`
	CookiesFile string `json:"cookiesFile,omitempty"`
}

func (a URLAuth) Validate() error {
	switch a.Browser {
	case "", "brave", "chrome", "chromium", "edge", "firefox", "opera", "safari", "vivaldi", "whale", "zen", "dia", "other":
	default:
		return errors.New("choose a supported browser for URL sign-in")
	}
	if a.Browser == "" && a.Profile != "" {
		return errors.New("choose a browser before specifying its profile")
	}
	fileMode := a.Browser == "dia" || a.Browser == "other"
	if fileMode && (a.CookiesFile == "" || a.Profile != "") {
		return errors.New("choose a Netscape cookies.txt export for Dia or an unlisted browser")
	}
	if !fileMode && a.CookiesFile != "" {
		return errors.New("choose Dia or Other browser to use a cookies.txt export")
	}
	if strings.ContainsAny(a.CookiesFile, "\x00\r\n") || strings.Contains(a.CookiesFile, "://") {
		return errors.New("choose a local cookies.txt file, not a URL")
	}
	if a.Browser == "safari" && a.Profile != "" {
		return errors.New("leave the profile blank when using Safari in Tala")
	}
	if strings.ContainsAny(a.Profile, "\x00\r\n") || strings.Contains(a.Profile, "::") {
		return errors.New("enter a browser profile name or path without control characters or container selectors")
	}
	return nil
}

func expandHome(path, home string) string {
	if strings.HasPrefix(path, "~/") {
		return filepath.Join(home, path[2:])
	}
	return path
}

// Zen uses Firefox's cookie schema. Pass an explicit Zen directory so yt-dlp
// never silently falls back to the user's Firefox account.
func (a URLAuth) browserSelector(home, platform string) (string, error) {
	if a.Browser != "zen" {
		if a.Profile != "" {
			return a.Browser + ":" + a.Profile, nil
		}
		return a.Browser, nil
	}
	profile := expandHome(a.Profile, home)
	if filepath.IsAbs(profile) {
		return "firefox:" + profile, nil
	}
	var roots []string
	switch platform {
	case "darwin":
		roots = []string{"Library/Application Support/zen/Profiles", "Library/Application Support/ZenBrowser/Profiles"}
	case "linux":
		roots = []string{".zen", ".var/app/app.zen_browser.zen/.zen", ".var/app/io.github.zen_browser.zen/.zen", ".config/zen"}
	default:
		return "", errors.New("enter the full Zen profile path from about:support")
	}
	for _, root := range roots {
		path := filepath.Join(home, root, profile)
		if info, err := os.Stat(path); err == nil && info.IsDir() {
			return "firefox:" + path, nil
		}
	}
	return "", errors.New("Zen profile not found; open about:support in Zen and paste its Profile Folder path")
}

// yt-dlp writes back to --cookies. Work on a private disposable copy so it
// cannot modify the user's export or leave updated account cookies alongside it.
func temporaryCookies(path string) (string, func(), error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", nil, err
	}
	in, err := os.Open(expandHome(path, home))
	if err != nil {
		return "", nil, fmt.Errorf("open cookies.txt: %w", err)
	}
	defer in.Close()
	info, err := in.Stat()
	if err != nil {
		return "", nil, err
	}
	const maxCookieBytes = 16 * 1024 * 1024
	if !info.Mode().IsRegular() || info.Size() > maxCookieBytes {
		return "", nil, errors.New("choose a regular Netscape cookies.txt file smaller than 16 MB")
	}
	reader := bufio.NewReader(io.LimitReader(in, maxCookieBytes+1))
	header, err := reader.ReadString('\n')
	if err != nil && err != io.EOF {
		return "", nil, errors.New("could not read cookies.txt header")
	}
	line := strings.TrimSpace(header)
	if line != "# Netscape HTTP Cookie File" && line != "# HTTP Cookie File" {
		return "", nil, errors.New("cookies.txt must be a Netscape cookie export, not JSON, a URL, or a browser database")
	}
	dir, err := os.MkdirTemp("", "tala-cookies-*")
	if err != nil {
		return "", nil, err
	}
	cleanup := func() { _ = os.RemoveAll(dir) }
	dest := filepath.Join(dir, "cookies.txt")
	out, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		cleanup()
		return "", nil, err
	}
	n, copyErr := io.Copy(out, io.MultiReader(strings.NewReader(header), reader))
	closeErr := out.Close()
	if copyErr != nil || closeErr != nil || n > maxCookieBytes {
		cleanup()
		return "", nil, errors.New("could not prepare cookies.txt; check that it is readable and smaller than 16 MB")
	}
	if err := validateCookieRows(dest); err != nil {
		cleanup()
		return "", nil, err
	}
	return dest, cleanup, nil
}

// Reject malformed exports before yt-dlp can include a cookie row in an error.
func validateCookieRows(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return errors.New("could not read the temporary cookie export")
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 4096), 16*1024*1024)
	invalid := errors.New("invalid Netscape cookie row; export cookies.txt again from your browser")
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "#HttpOnly_") {
			line = strings.TrimPrefix(line, "#HttpOnly_")
		} else if strings.HasPrefix(line, "#") || strings.TrimSpace(line) == "" {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) != 7 || fields[0] == "" ||
			(fields[1] != "TRUE" && fields[1] != "FALSE") ||
			(fields[3] != "TRUE" && fields[3] != "FALSE") ||
			(strings.HasPrefix(fields[0], ".") != (fields[1] == "TRUE")) {
			return invalid
		}
		if fields[4] != "" {
			if _, err := strconv.ParseInt(fields[4], 10, 64); err != nil {
				return invalid
			}
		}
	}
	if scanner.Err() != nil {
		return invalid
	}
	return nil
}

func ytDLPOutput(ctx context.Context, auth URLAuth, args ...string) ([]byte, error) {
	return ytDLPExecute(ctx, auth, args, func(args []string) ([]byte, error) { return output(ctx, "yt-dlp", args...) })
}

func ytDLPExecute(ctx context.Context, auth URLAuth, args []string, execute func([]string) ([]byte, error)) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := auth.Validate(); err != nil {
		return nil, err
	}
	prefix := []string{"--ignore-config", "--no-cookies"}
	if auth.CookiesFile != "" {
		path, cleanup, err := temporaryCookies(auth.CookiesFile)
		if err != nil {
			return nil, err
		}
		defer cleanup()
		prefix = append(prefix, "--cookies", path)
	} else if auth.Browser != "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		selector, err := auth.browserSelector(home, runtime.GOOS)
		if err != nil {
			return nil, err
		}
		prefix = append(prefix, "--cookies-from-browser", selector)
	}
	b, err := execute(append(prefix, args...))
	if err == nil || ctx.Err() != nil {
		return b, err
	}
	message := strings.ToLower(err.Error())
	if strings.Contains(message, "sign in") || strings.Contains(message, "sign-in") || strings.Contains(message, "login") || strings.Contains(message, "cookies") {
		return nil, fmt.Errorf("%w\nUse the URL sign-in helper to select the browser/profile or a fresh cookies.txt export, then fetch details or retry. If browser cookies cannot be read, close the browser and retry; check any macOS Keychain or file-access prompt. Some site restrictions still require an updated yt-dlp or remain unsupported", err)
	}
	return nil, err
}
