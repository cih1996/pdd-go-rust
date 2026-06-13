package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFindBundledADBPathInPrefersProjectADBDirectory(t *testing.T) {
	projectRoot := t.TempDir()
	executableDir := t.TempDir()

	projectADB := filepath.Join(projectRoot, "adb", adbExecutableName())
	if err := os.MkdirAll(filepath.Dir(projectADB), 0o755); err != nil {
		t.Fatalf("mkdir project adb dir: %v", err)
	}
	if err := os.WriteFile(projectADB, []byte("adb"), 0o644); err != nil {
		t.Fatalf("write project adb: %v", err)
	}

	exeADB := filepath.Join(executableDir, "adb", adbExecutableName())
	if err := os.MkdirAll(filepath.Dir(exeADB), 0o755); err != nil {
		t.Fatalf("mkdir exe adb dir: %v", err)
	}
	if err := os.WriteFile(exeADB, []byte("adb"), 0o644); err != nil {
		t.Fatalf("write exe adb: %v", err)
	}

	got, ok := findBundledADBPathIn(projectRoot, executableDir)
	if !ok {
		t.Fatal("expected bundled adb path to be found")
	}
	if got != projectADB {
		t.Fatalf("expected project adb path %q, got %q", projectADB, got)
	}
}

func TestFindBundledADBPathInFallsBackToExecutableDirectory(t *testing.T) {
	executableDir := t.TempDir()
	exeADB := filepath.Join(executableDir, "adb", adbExecutableName())
	if err := os.MkdirAll(filepath.Dir(exeADB), 0o755); err != nil {
		t.Fatalf("mkdir exe adb dir: %v", err)
	}
	if err := os.WriteFile(exeADB, []byte("adb"), 0o644); err != nil {
		t.Fatalf("write exe adb: %v", err)
	}

	got, ok := findBundledADBPathIn("", executableDir)
	if !ok {
		t.Fatal("expected executable-dir adb path to be found")
	}
	if got != exeADB {
		t.Fatalf("expected executable-dir adb path %q, got %q", exeADB, got)
	}
}
