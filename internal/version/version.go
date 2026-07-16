// Package version holds the harness build version and pin-check logic.
//
// A generated project pins an exact harness version in a committed
// `.harness-version` file. Every entry point compares the running binary's
// version against that pin: hooks warn on mismatch (never block editing) while
// CI and the git-hook delegates fail hard, so byte-identical scaffolding and
// rendering stay reproducible across machines even though the logic lives in an
// externally-installed CLI.
package version

import (
	"fmt"
	"strings"
)

// Version is the semantic version of this harness build. It is overridden at
// release time via -ldflags "-X github.com/olesho/harness/internal/version.Version=vX.Y.Z".
// The "0.0.0-dev" default marks an unreleased local build.
var Version = "0.0.0-dev"

// PinFileName is the committed file in a managed project that pins the exact
// harness version the project expects.
const PinFileName = ".harness-version"

// IsDev reports whether the running binary is an unreleased local build. Pin
// checks are advisory for dev builds so contributors can iterate without
// tripping the mismatch failure.
func IsDev() bool {
	return strings.HasPrefix(Version, "0.0.0")
}

// Match reports whether the running binary satisfies the given pin. An empty
// pin (no `.harness-version`) matches anything. A dev build matches any pin so
// local development is never blocked by the version gate.
func Match(pin string) bool {
	pin = normalize(pin)
	if pin == "" {
		return true
	}
	if IsDev() {
		return true
	}
	return normalize(Version) == pin
}

// Mismatch returns a human-readable, actionable message describing a pin
// mismatch, or the empty string when the running binary satisfies the pin.
func Mismatch(pin string) string {
	if Match(pin) {
		return ""
	}
	return fmt.Sprintf(
		"harness version mismatch: this project pins %s but the installed binary is %s.\n"+
			"Install the pinned version:\n\n    go install github.com/olesho/harness/cmd/harness@%s\n",
		normalize(pin), Version, normalize(pin),
	)
}

// normalize trims surrounding whitespace and a single leading "v" so that
// "v1.2.3", "1.2.3", and " 1.2.3\n" all compare equal.
func normalize(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "v")
	return s
}
