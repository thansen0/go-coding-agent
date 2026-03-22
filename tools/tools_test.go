package tools

import (
	"os"
	"path/filepath"
	"reflect"
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

func TestReadFileRange(t *testing.T) {
	root := t.TempDir()
	if err := SetWorkspaceRoot(root); err != nil {
		t.Fatalf("SetWorkspaceRoot: %v", err)
	}

	if _, err := WriteFile("notes.txt", "one\ntwo\nthree"); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	got, err := ReadFileRange("notes.txt", 2, 3)
	if err != nil {
		t.Fatalf("ReadFileRange: %v", err)
	}
	if got != "2: two\n3: three" {
		t.Fatalf("ReadFileRange = %q", got)
	}
}

func TestApplyPatch(t *testing.T) {
	root := t.TempDir()
	if err := SetWorkspaceRoot(root); err != nil {
		t.Fatalf("SetWorkspaceRoot: %v", err)
	}

	if _, err := WriteFile("notes.txt", "alpha\nbeta\ngamma"); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if _, err := ApplyPatch("notes.txt", "beta", "delta"); err != nil {
		t.Fatalf("ApplyPatch: %v", err)
	}

	got, err := ReadFile("notes.txt")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if got != "alpha\ndelta\ngamma" {
		t.Fatalf("patched file = %q", got)
	}
}

func TestListFilesAndSearchFiles(t *testing.T) {
	root := t.TempDir()
	if err := SetWorkspaceRoot(root); err != nil {
		t.Fatalf("SetWorkspaceRoot: %v", err)
	}

	if err := os.Mkdir(filepath.Join(root, "pkg"), 0o755); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	if _, err := WriteFile("pkg/main.go", "package pkg\n\nfunc Example() {}\n"); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	listing, err := ListFiles(".go", 20)
	if err != nil {
		t.Fatalf("ListFiles: %v", err)
	}
	if !strings.Contains(listing, "pkg/main.go") {
		t.Fatalf("ListFiles output = %q", listing)
	}

	matches, err := SearchFiles("Example", "*.go", 10)
	if err != nil {
		t.Fatalf("SearchFiles: %v", err)
	}
	if !strings.Contains(matches, "pkg/main.go:3:func Example() {}") {
		t.Fatalf("SearchFiles output = %q", matches)
	}
}

func TestRunCommandBlocksDangerousPrograms(t *testing.T) {
	root := t.TempDir()
	if err := SetWorkspaceRoot(root); err != nil {
		t.Fatalf("SetWorkspaceRoot: %v", err)
	}

	if _, err := RunCommand("sudo ls", "", "verification"); err == nil {
		t.Fatal("RunCommand allowed sudo")
	}

	if _, err := RunCommand("curl https://example.com", "", "verification"); err == nil {
		t.Fatal("RunCommand allowed curl")
	}
}

func TestRunCommandStaysWithinWorkspace(t *testing.T) {
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

	out, err := RunCommand("pwd", "project", "inspection")
	if err != nil {
		t.Fatalf("RunCommand pwd: %v", err)
	}
	if strings.TrimSpace(out) != subdir {
		t.Fatalf("pwd output = %q, want %q", strings.TrimSpace(out), subdir)
	}

	if _, err := RunCommand("pwd", "../", "inspection"); err == nil {
		t.Fatal("RunCommand allowed dir outside workspace")
	}
}

func TestAllowedGoActionsMatchWorkspaceVerificationPolicy(t *testing.T) {
	tests := map[string][]string{
		"build": {"go", "build", "./..."},
		"test":  {"go", "test", "./..."},
	}

	for action, want := range tests {
		if got := allowedGoActions[action]; !reflect.DeepEqual(got, want) {
			t.Fatalf("allowedGoActions[%q] = %#v, want %#v", action, got, want)
		}
	}
}
