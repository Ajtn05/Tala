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
const DefaultID = "large-v3-turbo"
const RecommendedID = "large-v3-turbo-q5"
const modelURL = "https://huggingface.co/ggerganov/whisper.cpp/resolve/main/"

type Model struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Use         string `json:"use"`
	Description string `json:"description"`
	Bytes       int64  `json:"bytes"`
	filename    string
	sha256      string
}

var catalog = []Model{
	{RecommendedID, "Whisper Large v3 Turbo Q5", "Everyday & live", "A smaller download for live recordings and everyday transcription.", 574041195, "ggml-large-v3-turbo-q5_0.bin", "394221709cd5ad1f40c46e6031ca61bce88931e6e088c188294c6d5a55ffa7e2"},
	{DefaultID, "Whisper Large v3 Turbo", "Full Turbo", "The original Turbo model for multilingual recording and imports.", 1624555275, Name, SHA256},
	{"large-v3", "Whisper Large v3", "Higher accuracy", "For difficult recordings, including Filipino and Taglish. Takes longer to process.", 3095033483, "ggml-large-v3.bin", "64d182b440b98d5203c4f9bd541544d84c605196c4f7b845dfa11fb23594d1e2"},
	{"small-q5", "Whisper Small Q5", "Low memory", "For machines with limited memory. Difficult speech may need more corrections.", 190085487, "ggml-small-q5_1.bin", "ae85e4a935d7a567bd102fe55afc16bb595bdb618e11b2fc7591bc08120411bb"},
}

func Catalog() []Model { return append([]Model(nil), catalog...) }

func Lookup(id string) (Model, bool) {
	for _, model := range catalog {
		if model.ID == id {
			return model, true
		}
	}
	return Model{}, false
}

func (s Settings) DefaultID() string {
	if _, ok := Lookup(s.DefaultModel); ok {
		return s.DefaultModel
	}
	return DefaultID
}

type Settings struct {
	DefaultModel     string        `json:"defaultModel,omitempty"`
	AskModel         bool          `json:"askModel,omitempty"`
	ModelPath        string        `json:"modelPath"`
	ModelDir         string        `json:"modelDir"`
	Language         string        `json:"language"`
	Prompt           string        `json:"prompt"`
	SkipQuietImports bool          `json:"skipQuietImports,omitempty"`
	MenuBarIcon      bool          `json:"menuBarIcon"`
	URLAuth          media.URLAuth `json:"urlAuth,omitempty"`
}
type Status struct {
	ID      string `json:"id,omitempty"`
	Managed bool   `json:"managed,omitempty"`
	Path    string `json:"path"`
	State   string `json:"state"`
	Detail  string `json:"detail"`
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
	model, _ := Lookup(s.DefaultID())
	return m.candidates(s, model)
}
func (m Manager) candidates(s Settings, model Model) []string {
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
			add(filepath.Join(d, model.filename))
		}
	}
	add(m.managedPath(model))
	add(m.recoveryPath(model))
	if model.ID == DefaultID {
		matches, _ := filepath.Glob(filepath.Join(m.Root(), "models", "large-v3-turbo-[0-9]*.bin"))
		for _, p := range matches {
			add(p)
		}
	}
	add(filepath.Join(filepath.Dir(m.Exe), "..", "Resources", "models", model.filename))
	add(filepath.Join(m.WorkingDir, "models", model.filename))
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
func (m Manager) managedPath(model Model) string {
	return filepath.Join(m.Root(), "models", model.filename)
}

func (m Manager) recoveryPath(model Model) string {
	return filepath.Join(m.Root(), "models", model.ID+"-verified.bin")
}

func (m Manager) managed(path string, model Model) bool {
	info, err := os.Lstat(path)
	return err == nil && info.Mode().IsRegular() &&
		(path == m.managedPath(model) || path == m.recoveryPath(model))
}

func Verify(ctx context.Context, path string) error {
	model, _ := Lookup(DefaultID)
	return verify(ctx, path, model)
}

func verify(ctx context.Context, path string, model Model) error {
	digest, err := checksum(ctx, path)
	if err != nil {
		return err
	}
	if digest != model.sha256 {
		return errors.New("SHA-256 mismatch; original file has been preserved")
	}
	return nil
}

func Identify(ctx context.Context, path string) (Model, error) {
	digest, err := checksum(ctx, path)
	if err != nil {
		return Model{}, err
	}
	for _, model := range catalog {
		if model.sha256 == digest {
			return model, nil
		}
	}
	return Model{}, errors.New("SHA-256 does not match a supported Whisper model")
}

func checksum(ctx context.Context, path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", errors.New("model is not a regular file")
	}
	var header [4]byte
	if _, err = io.ReadFull(f, header[:]); err != nil {
		return "", err
	}
	if string(header[:]) != "lmgg" {
		return "", errors.New("incompatible format: expected whisper.cpp GGML")
	}
	h := sha256.New()
	_, _ = h.Write(header[:])
	buf := make([]byte, 1024*1024)
	for {
		if err = ctx.Err(); err != nil {
			return "", err
		}
		n, e := f.Read(buf)
		if n > 0 {
			_, _ = h.Write(buf[:n])
		}
		if e == io.EOF {
			break
		}
		if e != nil {
			return "", e
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// Selection resolves an invocation override before the saved default.
func (m Manager) Selection(ctx context.Context, s Settings) (string, error) {
	if path := Environment("MODEL_PATH"); path != "" {
		model, err := Identify(ctx, m.expand(path))
		if err != nil {
			return "", fmt.Errorf("verify TALA_MODEL_PATH: %w", err)
		}
		return model.ID, nil
	}
	return s.DefaultID(), nil
}

func (m Manager) Discover(ctx context.Context) (Status, error) {
	s := m.Settings()
	id, err := m.Selection(ctx, s)
	if err != nil {
		if ctx.Err() != nil {
			return Status{}, ctx.Err()
		}
		// Keep searching the saved default if an environment override is invalid.
		id = s.DefaultID()
	}
	status, discoverErr := m.DiscoverModel(ctx, s, id)
	if discoverErr != nil && err != nil {
		status.Detail = strings.TrimSpace(err.Error() + "\n" + status.Detail)
	}
	return status, discoverErr
}

func (m Manager) DiscoverModel(ctx context.Context, s Settings, id string) (Status, error) {
	model, ok := Lookup(id)
	if !ok {
		return Status{ID: id, State: "missing"}, fmt.Errorf("unknown model %q", id)
	}
	var rejected []string
	for _, p := range m.candidates(s, model) {
		if err := ctx.Err(); err != nil {
			return Status{}, err
		}
		info, err := os.Stat(p)
		if err != nil {
			continue
		}
		if info.Size() != model.Bytes && (p == m.expand(s.ModelPath) || p == m.expand(Environment("MODEL_PATH"))) {
			continue
		}
		if err := verify(ctx, p, model); err == nil {
			return Status{ID: id, Path: p, State: "reused", Managed: m.managed(p, model), Detail: "Model verified and reused in place."}, nil
		} else {
			rejected = append(rejected, p+": "+err.Error())
		}
	}
	if err := ctx.Err(); err != nil {
		return Status{}, err
	}
	detail := strings.Join(rejected, "\n")
	if detail == "" {
		detail = "Download this model or choose an existing model in Settings."
	}
	return Status{ID: id, State: "missing", Detail: detail}, fmt.Errorf("%s is not installed", model.Name)
}

func (m Manager) Ensure(ctx context.Context, download bool, progress func(int64)) (Status, error) {
	status, err := m.Discover(ctx)
	if err == nil || !download || ctx.Err() != nil {
		return status, err
	}
	s := m.Settings()
	return m.Download(ctx, s, s.DefaultID(), progress)
}

// Download never overwrites an existing model. A failed download removes its temporary file.
func (m Manager) Download(ctx context.Context, s Settings, id string, progress func(int64)) (Status, error) {
	model, ok := Lookup(id)
	if !ok {
		return Status{}, fmt.Errorf("unknown model %q", id)
	}
	if status, err := m.DiscoverModel(ctx, s, id); err == nil {
		return status, nil
	}
	if err := ctx.Err(); err != nil {
		return Status{}, err
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
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, modelURL+model.filename, nil)
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
			total += int64(n)
			if total > model.Bytes {
				return Status{}, errors.New("model download exceeded the expected size")
			}
			if _, err = tmp.Write(buf[:n]); err != nil {
				return Status{}, err
			}
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
	if err = verify(ctx, tmp.Name(), model); err != nil {
		return Status{}, err
	}
	for _, path := range []string{m.managedPath(model), m.recoveryPath(model)} {
		if err = os.Link(tmp.Name(), path); err == nil {
			return Status{ID: id, Path: path, State: "downloaded", Managed: true, Detail: "Downloaded and verified with SHA-256."}, nil
		}
		if verify(ctx, path, model) == nil {
			return Status{ID: id, Path: path, State: "reused", Managed: m.managed(path, model), Detail: "A verified model already exists."}, nil
		}
	}
	return Status{}, fmt.Errorf("could not install %s without replacing an existing file: %w", model.Name, err)
}

func (m Manager) Remove(status Status) error {
	model, ok := Lookup(status.ID)
	if !ok || !m.managed(status.Path, model) {
		return errors.New("only models downloaded into Tala’s model folder can be removed here")
	}
	return os.Remove(status.Path)
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

// IdentifyPath accepts the same home-relative syntax as model discovery.
func (m Manager) IdentifyPath(ctx context.Context, path string) (Model, error) {
	return Identify(ctx, m.expand(path))
}

// EffectiveID gives a verified environment override priority over the preference.
func (m Manager) EffectiveID(s Settings, statuses map[string]Status) string {
	if override := Environment("MODEL_PATH"); override != "" {
		override = filepath.Clean(m.expand(override))
		for id, status := range statuses {
			if status.Path == override {
				return id
			}
		}
	}
	return s.DefaultID()
}
