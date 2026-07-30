package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRepositoryRoot(t *testing.T) {
	root, err := repositoryRoot()
	if err != nil {
		t.Fatalf("repositoryRoot() error = %v", err)
	}
	content, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		t.Fatalf("ReadFile(go.mod) error = %v", err)
	}
	if len(content) == 0 {
		t.Fatal("repositoryRoot() selected an empty go.mod")
	}
}

func TestIsRepositoryRoot(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	found, err := isRepositoryRoot(root)
	if err != nil {
		t.Fatalf("isRepositoryRoot() error = %v", err)
	}
	if found {
		t.Fatal("isRepositoryRoot() accepted a directory without go.mod")
	}
	err = os.WriteFile(
		filepath.Join(root, "go.mod"),
		[]byte(moduleDeclaration+"\n"),
		0o600,
	)
	if err != nil {
		t.Fatalf("WriteFile(go.mod) error = %v", err)
	}
	found, err = isRepositoryRoot(root)
	if err != nil {
		t.Fatalf("isRepositoryRoot() error = %v", err)
	}
	if !found {
		t.Fatal("isRepositoryRoot() did not recognize Spice module")
	}
}
