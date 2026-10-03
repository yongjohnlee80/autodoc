package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExportMainWritesChosenDestination(t *testing.T) {
	directory := t.TempDir()
	sourcePath := filepath.Join(directory, "note.md")
	destination := filepath.Join(directory, "note.html")
	if err := os.WriteFile(sourcePath, []byte("# Heading\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := exportMain(sourcePath, "html", destination, "dark"); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(destination)
	if err != nil || !strings.Contains(string(content), "<h1>Heading</h1>") {
		t.Fatalf("HTML export = %q, %v", content, err)
	}
	if err := exportMain(sourcePath, "text", sourcePath, "light"); err == nil {
		t.Fatal("export overwrote its source")
	}
}
