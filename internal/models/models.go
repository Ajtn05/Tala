// Package models discovers and verifies models before any download or inference.
package models

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"tala/internal/media"
)

const Name = "ggml-large-v3-turbo.bin"
const SHA256 = "1fc70f774d38eb169993ac391eea357ef47c88757ef72ee5943879b7e8e2bc69"
const URL = "https://huggingface.co/ggerganov/whisper.cpp/resolve/main/" + Name

type Settings struct {
	ModelPath        string        `json:"modelPath"`
	ModelDir         string        `json:"modelDir"`
	Language         string        `json:"language"`
	Prompt           string        `json:"prompt"`
	SkipQuietImports bool          `json:"skipQuietImports,omitempty"`
	MenuBarIcon      bool          `json:"menuBarIcon"`
	URLAuth          media.URLAuth `json:"urlAuth,omitempty"`
}
type Status struct {
	Path   string `json:"path"`
	State  string `json:"state"`
	Detail string `json:"detail"`
}
type Manager struct{ Home, Exe, WorkingDir string }

func Default() Manager {
	h, _ := os.UserHomeDir()
	e, _ := os.Executable()
	w, _ := os.Getwd()
	return Manager{h, e, w}
}
func (m Manager) Root() string {
	if root := Environment("DATA_DIR"); root != "" {
		return root
	}
	if runtime.GOOS == "linux" {
		return xdgPath("XDG_DATA_HOME", filepath.Join(m.Home, ".local", "share"), "tala")
	}
	return filepath.Join(m.Home, "Library", "Application Support", "Tala")
}
func (m Manager) ConfigPath() string {
	if runtime.GOOS == "linux" && Environment("DATA_DIR") == "" {
		return xdgPath("XDG_CONFIG_HOME", filepath.Join(m.Home, ".config"), "tala", "settings.json")
	}
	return filepath.Join(m.Root(), "settings.json")
}
func xdgPath(key, fallback string, parts ...string) string {
	base := os.Getenv(key)
	if !filepath.IsAbs(base) {
		base = fallback
	}
	return filepath.Join(append([]string{base}, parts...)...)
}
func (m Manager) OutputDir() string {
	if dir := Environment("OUTPUT_DIR"); dir != "" {
		return dir
	}
	if runtime.GOOS == "linux" {
		return filepath.Join(m.Root(), "transcripts")
	}
	return filepath.Join(m.Home, "Documents", "Tala")
}
func (m Manager) Settings() Settings {
	var s Settings
	if b, err := os.ReadFile(m.ConfigPath()); err == nil {
		_ = json.Unmarshal(b, &s)
	}
	if s.Language == "" {
		s.Language = "auto"
	}
	return s
}
func (m Manager) Save(s Settings) error {
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return AtomicWrite(m.ConfigPath(), b)
}
func (m Manager) expand(p string) string {
	if strings.HasPrefix(p, "~/") {
		return filepath.Join(m.Home, p[2:])
	}
	return p
}
func (m Manager) Candidates(s Settings) []string {
	var paths []string
	add := func(p string) {
		if p != "" {
			paths = append(paths, m.expand(p))
		}
	}
	add(os.Getenv("TALA_MODEL_PATH"))
	add(s.ModelPath)
	for _, d := range []string{os.Getenv("TALA_MODEL_DIR"), s.ModelDir} {
		if d != "" {
			add(filepath.Join(d, Name))
		}
	}
	add(filepath.Join(m.Root(), "models", Name))
	add(filepath.Join(filepath.Dir(m.Exe), "..", "Resources", "models", Name))
	add(filepath.Join(m.WorkingDir, "models", Name))
	seen := map[string]bool{}
	var unique []string
	for _, p := range paths {
		p = filepath.Clean(p)
		if !seen[p] {
			unique = append(unique, p)
			seen[p] = true
		}
	}
	return unique
}
func Verify(ctx context.Context, path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errors.New("model is not a regular file")
	}
	var header [4]byte
	if _, err = io.ReadFull(f, header[:]); err != nil {
		return err
	}
	if string(header[:]) != "lmgg" {
		return errors.New("incompatible format: expected whisper.cpp GGML")
	}
	h := sha256.New()
	_, _ = h.Write(header[:])
	buf := make([]byte, 1024*1024)
	for {
		if err = ctx.Err(); err != nil {
			return err
		}
		n, e := f.Read(buf)
		if n > 0 {
			_, _ = h.Write(buf[:n])
		}
		if e == io.EOF {
			break
		}
		if e != nil {
			return e
		}
	}
	if hex.EncodeToString(h.Sum(nil)) != SHA256 {
		return errors.New("SHA-256 mismatch; original file has been preserved")
	}
	return nil
}
func (m Manager) Discover(ctx context.Context) (Status, error) {
	var rejected []string
	for _, p := range m.Candidates(m.Settings()) {
		if _, err := os.Stat(p); err != nil {
			continue
		}
		if err := Verify(ctx, p); err == nil {
			return Status{p, "reused", "Existing model verified with SHA-256 and reused in place."}, nil
		} else {
			rejected = append(rejected, p+": "+err.Error())
		}
		if ctx.Err() != nil {
			return Status{}, ctx.Err()
		}
	}
	return Status{State: "missing", Detail: strings.Join(rejected, "\n")}, errors.New("no compatible verified model found")
}

// Ensure never overwrites an existing model. A failed download only removes its own temporary file.
func (m Manager) Ensure(ctx context.Context, download bool, progress func(int64)) (Status, error) {
	if status, err := m.Discover(ctx); err == nil {
		return status, nil
	}
	if err := ctx.Err(); err != nil {
		return Status{}, err
	}
	if !download {
		return Status{State: "missing"}, errors.New("choose an existing model or download the model in Settings")
	}
	dir := filepath.Join(m.Root(), "models")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return Status{}, err
	}
	tmp, err := os.CreateTemp(dir, ".download-*.bin")
	if err != nil {
		return Status{}, err
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, URL, nil)
	if err != nil {
		return Status{}, err
	}
	client := &http.Client{Timeout: 2 * time.Hour}
	res, err := client.Do(req)
	if err != nil {
		return Status{}, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return Status{}, fmt.Errorf("model download: HTTP %d", res.StatusCode)
	}
	buf := make([]byte, 1024*1024)
	var total int64
	for {
		n, e := res.Body.Read(buf)
		if n > 0 {
			if _, err = tmp.Write(buf[:n]); err != nil {
				return Status{}, err
			}
			total += int64(n)
			if progress != nil {
				progress(total)
			}
		}
		if e == io.EOF {
			break
		}
		if e != nil {
			return Status{}, e
		}
	}
	if err = tmp.Sync(); err != nil {
		return Status{}, err
	}
	_ = tmp.Close()
	if err = Verify(ctx, tmp.Name()); err != nil {
		return Status{}, err
	}
	path := filepath.Join(dir, Name)
	// Link is atomic and refuses to replace an existing (even invalid) model.
	if err = os.Link(tmp.Name(), path); err != nil {
		if Verify(ctx, path) == nil {
			return Status{path, "reused", "A verified model already exists."}, nil
		}
		path = filepath.Join(dir, fmt.Sprintf("large-v3-turbo-%d.bin", time.Now().UnixNano()))
		if err = os.Link(tmp.Name(), path); err != nil {
			return Status{}, err
		}
	}
	s := m.Settings()
	s.ModelPath = path
	if err = m.Save(s); err != nil {
		return Status{}, err
	}
	return Status{path, "downloaded", "Downloaded and verified with SHA-256. Stored outside the application bundle."}, nil
}

func AtomicWrite(path string, b []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".tala-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if _, err = f.Write(b); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

// Environment returns a Tala configuration override.
func Environment(name string) string {
	return os.Getenv("TALA_" + name)
}
