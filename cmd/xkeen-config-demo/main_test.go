package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestDemoCatalogAndWrite(t *testing.T) {
	got, issues := buildDemoCatalog(false)
	if got == nil || len(issues) != 0 {
		t.Fatalf("missing demo: %+v", issues)
	}
	if len(got.Files) != 6 || !bytes.Contains(got.Files[3].Content, []byte(`9007199254740993`)) || !bytes.Contains(got.Files[4].Content, []byte(`demo-local`)) {
		t.Fatal("incomplete demo")
	}
	dir := t.TempDir()
	if err := writeCatalog(dir, got); err != nil {
		t.Fatal(err)
	}
	files, err := os.ReadDir(dir)
	if err != nil || len(files) != 6 {
		t.Fatalf("%+v %v", files, err)
	}
	for _, file := range got.Files {
		raw, err := os.ReadFile(filepath.Join(dir, file.Name))
		if err != nil || !bytes.Equal(raw, file.Content) {
			t.Fatalf("changed candidate %s: %v", file.Name, err)
		}
	}
	invalid, issues := buildDemoCatalog(true)
	if invalid == nil || len(issues) != 0 || !bytes.Contains(invalid.Files[3].Content, []byte(`invalid-demo-protocol`)) {
		t.Fatal("negative domain candidate missing")
	}
}

func TestDemoRejectsNonemptyAndWriteError(t *testing.T) {
	got, issues := buildDemoCatalog(false)
	if got == nil || len(issues) != 0 {
		t.Fatal(issues)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "previous.json")
	if err := os.WriteFile(path, []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := writeCatalog(dir, got); err == nil {
		t.Fatal("mixed previous files")
	}
	files, _ := os.ReadDir(dir)
	if len(files) != 1 {
		t.Fatal("changed old directory")
	}
	if err := writeCatalog(filepath.Join(dir, "missing"), got); err == nil {
		t.Fatal("false IO success")
	}
}

func TestDemoCLIAndRollback(t *testing.T) {
	if err := run(nil); err == nil {
		t.Fatal("missing directory accepted")
	}
	dir := t.TempDir()
	if err := run([]string{"-dir", dir}); err != nil {
		t.Fatal(err)
	}
	got, _ := buildDemoCatalog(false)
	got.Files[1].Name = "../outside.json"
	dir = t.TempDir()
	if err := writeCatalog(dir, got); err == nil {
		t.Fatal("unsafe filename accepted")
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Fatal("failed write left partial files")
	}
}
