package version

import "testing"

func TestMatch(t *testing.T) {
	orig := Version
	t.Cleanup(func() { Version = orig })

	tests := []struct {
		name    string
		running string
		pin     string
		want    bool
	}{
		{"empty pin matches", "1.2.3", "", true},
		{"exact match", "1.2.3", "1.2.3", true},
		{"v-prefix normalized on both sides", "v1.2.3", "1.2.3", true},
		{"v-prefix on pin", "1.2.3", "v1.2.3", true},
		{"whitespace trimmed", "1.2.3", " 1.2.3\n", true},
		{"mismatch", "1.2.3", "1.2.4", false},
		{"dev build matches any pin", "0.0.0-dev", "9.9.9", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			Version = tc.running
			if got := Match(tc.pin); got != tc.want {
				t.Fatalf("Match(%q) with running %q = %v, want %v", tc.pin, tc.running, got, tc.want)
			}
		})
	}
}

func TestMismatchMessage(t *testing.T) {
	orig := Version
	t.Cleanup(func() { Version = orig })

	Version = "1.2.3"
	if msg := Mismatch("1.2.3"); msg != "" {
		t.Fatalf("expected no message on match, got %q", msg)
	}
	msg := Mismatch("2.0.0")
	if msg == "" {
		t.Fatal("expected a mismatch message")
	}
	for _, want := range []string{"1.2.3", "2.0.0", "go install"} {
		if !contains(msg, want) {
			t.Fatalf("mismatch message %q missing %q", msg, want)
		}
	}
}

func TestIsDev(t *testing.T) {
	orig := Version
	t.Cleanup(func() { Version = orig })

	Version = "0.0.0-dev"
	if !IsDev() {
		t.Fatal("expected dev build")
	}
	Version = "1.0.0"
	if IsDev() {
		t.Fatal("expected non-dev build")
	}
}

func contains(haystack, needle string) bool {
	return len(needle) == 0 || indexOf(haystack, needle) >= 0
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
