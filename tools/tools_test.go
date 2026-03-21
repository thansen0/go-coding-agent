package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadWriteFileStayWithinWorkspace(t *testing.T) {
	root := t.TempDir()
	if err := SetWorkspaceRoot(root); err != nil {
		t.Fatalf("SetWorkspaceRoot: %v", err)
	}

	if _, err := WriteFile("notes.txt", "hello"); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	got, err := ReadFile("notes.txt")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if got != "hello" {
		t.Fatalf("ReadFile = %q, want %q", got, "hello")
	}

	if _, err := ReadFile("../outside.txt"); err == nil {
		t.Fatal("ReadFile allowed path outside workspace")
	}
}

func TestRunShellBlocksDangerousPrograms(t *testing.T) {
	root := t.TempDir()
	if err := SetWorkspaceRoot(root); err != nil {
		t.Fatalf("SetWorkspaceRoot: %v", err)
	}

	if _, err := RunShell("sudo ls", ""); err == nil {
		t.Fatal("RunShell allowed sudo")
	}

	if _, err := RunShell("curl https://example.com", ""); err == nil {
		t.Fatal("RunShell allowed curl")
	}
}

func TestRunShellStaysWithinWorkspace(t *testing.T) {
	root := t.TempDir()
	if err := SetWorkspaceRoot(root); err != nil {
		t.Fatalf("SetWorkspaceRoot: %v", err)
	}

	subdir := filepath.Join(root, "project")
	if err := os.Mkdir(subdir, 0o755); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}

	if _, err := WriteFile("project/file.txt", "content"); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	out, err := RunShell("pwd", "project")
	if err != nil {
		t.Fatalf("RunShell pwd: %v", err)
	}
	if strings.TrimSpace(out) != subdir {
		t.Fatalf("pwd output = %q, want %q", strings.TrimSpace(out), subdir)
	}

	if _, err := RunShell("pwd", "../"); err == nil {
		t.Fatal("RunShell allowed dir outside workspace")
	}
}
