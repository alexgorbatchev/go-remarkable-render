package render_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestCleanPreservesConcurrentScratch(t *testing.T) {
	justfile, err := os.ReadFile("Justfile")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Justfile"), justfile, 0600); err != nil {
		t.Fatal(err)
	}
	artifacts := []string{"bin/app", "coverage.out", "coverage.html", "render.test"}
	scratch := []string{".tmp/render/input.pdf", ".tmp/other-task/report.txt"}
	for _, name := range append(artifacts, scratch...) {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(name), 0600); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command("just", "clean")
	cmd.Dir = dir
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("just clean: %v\n%s", err, output)
	}
	for _, name := range artifacts {
		if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Errorf("artifact %s remains: %v", name, err)
		}
	}
	for _, name := range scratch {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil || string(data) != name {
			t.Errorf("scratch %s changed: %q, %v", name, data, err)
		}
	}
}
