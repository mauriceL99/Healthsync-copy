package agent

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestWriteNewPrivateTightensModeOnOverwrite(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX file modes")
	}
	path := filepath.Join(t.TempDir(), "export.json")
	if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writeNewPrivate(path, []byte("new"), false); err == nil {
		t.Fatal("existing file overwritten without --force")
	}
	if err := writeNewPrivate(path, []byte("new"), true); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode after --force: %v", info.Mode().Perm())
	}
	if data, _ := os.ReadFile(path); string(data) != "new" {
		t.Fatalf("content: %q", data)
	}
}
