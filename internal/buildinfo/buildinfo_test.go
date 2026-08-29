package buildinfo

import "testing"

func TestString(t *testing.T) {
	oldVersion := Version
	Version = "v1.2.3"
	t.Cleanup(func() { Version = oldVersion })

	if got, want := String(), "framekit v1.2.3"; got != want {
		t.Fatalf("String() = %q, want %q", got, want)
	}
}
