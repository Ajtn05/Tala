package media

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestDependencyReadinessByWorkflow(t *testing.T) {
	cases := []struct {
		name                    string
		installed               []string
		fileReady, urlReady     bool
		fileCommand, urlCommand string
	}{
		{"none", nil, false, false, "brew install ffmpeg", "brew install ffmpeg yt-dlp"},
		{"local only", []string{"ffmpeg", "ffprobe"}, true, false, "", "brew install yt-dlp"},
		{"downloader only", []string{"yt-dlp"}, false, false, "brew install ffmpeg", "brew install ffmpeg"},
		{"missing ffprobe", []string{"ffmpeg", "yt-dlp"}, false, false, "brew install ffmpeg", "brew install ffmpeg"},
		{"all", []string{"ffmpeg", "ffprobe", "yt-dlp"}, true, true, "", ""},
	}
	for _, tc := range cases {
		if runtime.GOOS == "linux" {
			for _, command := range []*string{&tc.fileCommand, &tc.urlCommand} {
				if *command != "" {
					*command = packageInstallCommand("linux", strings.Fields(strings.TrimPrefix(*command, "brew install ")))
				}
			}
		}
		t.Run(tc.name, func(t *testing.T) {
			report := dependencies(func(name string) (string, error) {
				for _, installed := range tc.installed {
					if installed == name {
						return "/tools/" + name, nil
					}
				}
				return "", os.ErrNotExist
			})
			if report.File.Ready != tc.fileReady || report.URL.Ready != tc.urlReady {
				t.Fatalf("wrong workflow readiness: %+v", report)
			}
			if report.File.InstallCommand != tc.fileCommand || report.URL.InstallCommand != tc.urlCommand {
				t.Fatalf("wrong install instructions: %+v", report)
			}
			if (report.Require("file") == nil) != tc.fileReady || (report.Require("url") == nil) != tc.urlReady {
				t.Fatal("import guard disagrees with readiness")
			}
			if report.Require("live") != nil {
				t.Fatal("media tools blocked live recording")
			}
			if !tc.urlReady {
				err := report.Require("url")
				if !strings.Contains(err.Error(), tc.urlCommand) {
					t.Fatal("missing actionable install instructions")
				}
			}
		})
	}
}
func TestToolDiscoveryAfterInstallation(t *testing.T) {
	dir := t.TempDir()
	noPath := func(string) (string, error) { return "", os.ErrNotExist }
	lookup := func(name string) (string, error) { return findTool(name, noPath, []string{dir}) }
	if dependencies(lookup).File.Ready {
		t.Fatal("reported tools before installation")
	}
	for _, name := range []string{"ffmpeg", "ffprobe"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	report := dependencies(lookup)
	if !report.File.Ready || report.URL.Ready {
		t.Fatalf("recheck did not find newly installed local tools: %+v", report)
	}
	if report.Tools["ffmpeg"].Path != filepath.Join(dir, "ffmpeg") {
		t.Fatal("resolved tool path was lost")
	}
}
func TestFallbackRejectsDirectoriesAndNonExecutableFiles(t *testing.T) {
	dir := t.TempDir()
	noPath := func(string) (string, error) { return "", os.ErrNotExist }
	if err := os.Mkdir(filepath.Join(dir, "ffmpeg"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := findTool("ffmpeg", noPath, []string{dir}); err == nil {
		t.Fatal("accepted an executable directory")
	}
	if err := os.WriteFile(filepath.Join(dir, "ffprobe"), []byte("not executable"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := findTool("ffprobe", noPath, []string{dir}); err == nil {
		t.Fatal("accepted a non-executable file")
	}
}
func TestToolUsesPATHBeforeFallback(t *testing.T) {
	path, err := findTool("ffmpeg", func(string) (string, error) { return "/configured/ffmpeg", nil }, nil)
	if err != nil || path != "/configured/ffmpeg" {
		t.Fatalf("PATH was not preferred: %q %v", path, err)
	}
	if _, err = findTool("ffprobe", func(string) (string, error) { return "", errors.New("absent") }, nil); err == nil || !strings.Contains(err.Error(), installCommand([]string{"ffprobe"})) {
		t.Fatal("ffprobe should be supplied by the ffmpeg package")
	}
}
