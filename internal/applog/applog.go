package applog

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const logDirName = "logs"

// Open creates one log file for the current process.
func Open() (*os.File, string, error) {
	dir, err := filepath.Abs(logDirName)
	if err != nil {
		return nil, "", fmt.Errorf("resolving log directory: %w", err)
	}
	return openInDir(dir)
}

// OpenPath opens an existing session log or creates it when needed.
func OpenPath(path string) (*os.File, string, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, "", fmt.Errorf("resolving debug log path: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
		return nil, "", fmt.Errorf("creating log directory: %w", err)
	}
	f, err := os.OpenFile(absPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, "", fmt.Errorf("opening debug log: %w", err)
	}
	return f, absPath, nil
}

func openInDir(dir string) (*os.File, string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, "", fmt.Errorf("creating log directory: %w", err)
	}
	name := fmt.Sprintf("framekit-%s-%d.log", time.Now().Format("20060102-150405.000"), os.Getpid())
	path := filepath.Join(dir, name)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, "", fmt.Errorf("opening debug log: %w", err)
	}
	return f, path, nil
}

// DiagnosticsDir returns the persistent directory used for failed input files.
func DiagnosticsDir() (string, error) {
	dir, err := filepath.Abs(filepath.Join(logDirName, "diagnostics"))
	if err != nil {
		return "", fmt.Errorf("resolving diagnostics directory: %w", err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("creating diagnostics directory: %w", err)
	}
	return dir, nil
}
