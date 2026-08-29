package buildinfo

// Version is replaced by the build with a Git tag or commit description.
var Version = "dev"

// String returns the user-facing application name and version.
func String() string {
	return "framekit " + Version
}
