package media

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

type Dependency struct {
	Available bool   `json:"available"`
	Path      string `json:"path,omitempty"`
}
type Readiness struct {
	Ready          bool     `json:"ready"`
	Missing        []string `json:"missing"`
	InstallCommand string   `json:"installCommand,omitempty"`
}
type DependencyReport struct {
	Tools map[string]Dependency `json:"tools"`
	File  Readiness             `json:"file"`
	URL   Readiness             `json:"url"`
}

// Dependencies checks executables without starting tools or installing software.
// Release packages supply their own tools. Source builds can use system tools.
func Dependencies() DependencyReport { return dependencies(Tool) }
func dependencies(lookup func(string) (string, error)) DependencyReport {
	report := DependencyReport{Tools: map[string]Dependency{}}
	for _, name := range []string{"ffmpeg", "ffprobe", "yt-dlp"} {
		path, err := lookup(name)
		report.Tools[name] = Dependency{Available: err == nil, Path: path}
	}
	report.File = readiness([]string{"ffmpeg", "ffprobe"}, report.Tools)
	report.URL = readiness([]string{"ffmpeg", "ffprobe", "yt-dlp"}, report.Tools)
	return report
}
func readiness(names []string, tools map[string]Dependency) Readiness {
	status := Readiness{Ready: true, Missing: []string{}}
	for _, name := range names {
		if !tools[name].Available {
			status.Missing = append(status.Missing, name)
		}
	}
	status.Ready = len(status.Missing) == 0
	if !status.Ready {
		status.InstallCommand = installCommand(status.Missing)
	}
	return status
}
func installCommand(names []string) string {
	var packages []string
	for _, name := range names {
		pkg := name
		if pkg == "ffprobe" {
			pkg = "ffmpeg"
		}
		found := false
		for _, added := range packages {
			if added == pkg {
				found = true
			}
		}
		if !found {
			packages = append(packages, pkg)
		}
	}
	return packageInstallCommand(runtime.GOOS, packages)
}
func packageInstallCommand(platform string, packages []string) string {
	if platform == "linux" {
		return "sudo apt install " + strings.Join(packages, " ") + " (Debian/Ubuntu; use your distribution's package manager elsewhere)"
	}
	return "brew install " + strings.Join(packages, " ")
}

// Require rejects an unsupported import before downloading media or loading Whisper.
// Live recording intentionally has no FFmpeg or yt-dlp requirement.
func (report DependencyReport) Require(kind string) error {
	var status Readiness
	switch kind {
	case "live":
		return nil
	case "file":
		status = report.File
	case "url":
		status = report.URL
	default:
		return fmt.Errorf("unknown media workflow: %s", kind)
	}
	if status.Ready {
		return nil
	}
	return fmt.Errorf("missing %s for %s imports. Install with: %s", strings.Join(status.Missing, ", "), kind, status.InstallCommand)
}
func Tool(name string) (string, error) {
	if path := bundledTool(name); path != "" {
		return path, nil
	}
	return findTool(name, exec.LookPath, []string{"/opt/homebrew/bin", "/usr/local/bin"})
}

func bundledTool(name string) string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	dir := filepath.Dir(exe)
	if runtime.GOOS == "darwin" {
		return executableIn(name, []string{
			filepath.Join(dir, "..", "Helpers"),
			filepath.Join(dir, "..", "Resources", "tools"),
		})
	}
	return executableIn(name, []string{filepath.Join(dir, "tala-tools")})
}

func findTool(name string, lookPath func(string) (string, error), directories []string) (string, error) {
	if path, err := lookPath(name); err == nil {
		return path, nil
	}
	if path := executableIn(name, directories); path != "" {
		return path, nil
	}
	return "", fmt.Errorf("%s is needed for this import. Install with: %s", name, installCommand([]string{name}))
}

func executableIn(name string, directories []string) string {
	for _, directory := range directories {
		path := filepath.Join(directory, name)
		if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() && info.Mode()&0111 != 0 {
			return path
		}
	}
	return ""
}
