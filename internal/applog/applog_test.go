package applog

import (
	"os"
	"path/filepath"
	"testing"
)

func TestOpenInDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "logs")
	f, path, err := openInDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("test log\n"); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() == 0 {
		t.Fatal("log file is empty")
	}
}

func TestOpenPathAppendsToSessionLog(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.log")
	launcher, gotPath, err := OpenPath(path)
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != path {
		t.Fatalf("OpenPath() path = %q, want %q", gotPath, path)
	}
	elevated, _, err := OpenPath(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := launcher.WriteString("launcher\n"); err != nil {
		t.Fatal(err)
	}
	if _, err := elevated.WriteString("elevated\n"); err != nil {
		t.Fatal(err)
	}
	if err := elevated.Close(); err != nil {
		t.Fatal(err)
	}
	if err := launcher.Close(); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "launcher\nelevated\n" {
		t.Fatalf("session log = %q", data)
	}
}
