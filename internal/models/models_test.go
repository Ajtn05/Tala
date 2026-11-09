package models

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestCandidatesIncludeConfiguredAndWorkspace(t *testing.T) {
	t.Setenv("TALA_DATA_DIR", t.TempDir())
	t.Setenv("TALA_MODEL_PATH", "/external/model.bin")
	t.Setenv("TALA_MODEL_DIR", "/external/models")
	m := Manager{Home: "/Users/test", Exe: "/Applications/Tala.app/Contents/MacOS/tala", WorkingDir: "/workspace"}
	paths := m.Candidates(Settings{ModelPath: "~/my-model.bin", ModelDir: "/configured"})
	joined := strings.Join(paths, "\n")
	for _, p := range []string{"/external/model.bin", "/external/models/" + Name, "/Users/test/my-model.bin", "/configured/" + Name, filepath.Join(m.Root(), "models", Name), "/workspace/models/" + Name} {
		if !strings.Contains(joined, p) {
			t.Errorf("missing candidate %s", p)
		}
	}
	if paths[0] != "/external/model.bin" {
		t.Fatal("explicit override must be preferred")
	}
}
func TestRejectIncompatibleAndCorruptWithoutModification(t *testing.T) {
	for _, data := range []string{"GGUFbad", "lmggcorrupt", ""} {
		p := filepath.Join(t.TempDir(), "model.bin")
		if e := os.WriteFile(p, []byte(data), 0600); e != nil {
			t.Fatal(e)
		}
		if Verify(context.Background(), p) == nil {
			t.Fatal("accepted invalid model")
		}
		after, _ := os.ReadFile(p)
		if string(after) != data {
			t.Fatal("original model modified")
		}
	}
}
func TestCancelledVerification(t *testing.T) {
	p := filepath.Join(t.TempDir(), "model.bin")
	_ = os.WriteFile(p, []byte("lmggcorrupt"), 0600)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if Verify(ctx, p) != context.Canceled {
		t.Fatal("verification did not cancel")
	}
}
func TestEnsureWithoutDownloadPreservesAllFiles(t *testing.T) {
	home := t.TempDir()
	root := t.TempDir()
	t.Setenv("TALA_DATA_DIR", root)
	t.Setenv("TALA_MODEL_PATH", filepath.Join(home, "invalid.bin"))
	_ = os.WriteFile(filepath.Join(home, "invalid.bin"), []byte("lmggbad"), 0600)
	m := Manager{Home: home, Exe: "/nonexistent", WorkingDir: home}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e := m.Ensure(ctx, true, nil); e != context.Canceled {
		t.Fatalf("unexpected error %v", e)
	}
	b, _ := os.ReadFile(filepath.Join(home, "invalid.bin"))
	if string(b) != "lmggbad" {
		t.Fatal("original changed")
	}
	entries, _ := os.ReadDir(root)
	if len(entries) != 0 {
		t.Fatal("cancelled discovery created download files")
	}
}
func TestSettingsSurviveNewManager(t *testing.T) {
	t.Setenv("TALA_DATA_DIR", t.TempDir())
	m := Manager{}
	if m.Settings().MenuBarIcon {
		t.Fatal("menu bar icon must default to off")
	}
	want := Settings{ModelPath: "/original/model.bin", ModelDir: "/models", Language: "auto", Prompt: "Taglish", MenuBarIcon: true}
	if e := m.Save(want); e != nil {
		t.Fatal(e)
	}
	if got := (Manager{}).Settings(); got != want {
		t.Fatalf("got %+v", got)
	}
}
func TestLinuxXDGPaths(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux platform defaults")
	}
	t.Setenv("TALA_DATA_DIR", "")
	t.Setenv("TALA_OUTPUT_DIR", "")
	t.Setenv("XDG_DATA_HOME", "/xdg/data")
	t.Setenv("XDG_CONFIG_HOME", "/xdg/config")
	m := Manager{Home: "/home/test"}
	if m.Root() != "/xdg/data/tala" || m.ConfigPath() != "/xdg/config/tala/settings.json" || m.OutputDir() != "/xdg/data/tala/transcripts" {
		t.Fatalf("wrong XDG paths: %s %s %s", m.Root(), m.ConfigPath(), m.OutputDir())
	}
	t.Setenv("XDG_DATA_HOME", "relative/is/invalid")
	if m.Root() != "/home/test/.local/share/tala" {
		t.Fatal("relative XDG path was accepted")
	}
	t.Setenv("TALA_DATA_DIR", "/isolated")
	if m.ConfigPath() != "/isolated/settings.json" {
		t.Fatal("data override did not isolate settings")
	}
}
func TestInstalledModelIntegration(t *testing.T) {
	path := Environment("TEST_MODEL")
	if path == "" {
		t.Skip("set TALA_TEST_MODEL for a full checksum and reuse integration test")
	}
	t.Setenv("TALA_MODEL_PATH", path)
	m := Default()
	before, e := os.Stat(path)
	if e != nil {
		t.Fatal(e)
	}
	status, e := m.Ensure(context.Background(), true, nil)
	if e != nil {
		t.Fatal(e)
	}
	if status.State != "reused" || status.Path != path {
		t.Fatalf("unexpected reuse: %+v", status)
	}
	after, _ := os.Stat(path)
	if before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
		t.Fatal("model was changed")
	}
}
