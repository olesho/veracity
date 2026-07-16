package ownership

import "testing"

func TestDecide(t *testing.T) {
	m := New()
	rendered := []byte("v2")
	m.Set("hooks/x.sh", Managed, []byte("v1")) // last-generated was v1

	tests := []struct {
		name   string
		rel    string
		kind   Kind
		onDisk []byte
		want   Decision
	}{
		{"absent -> create", "hooks/x.sh", Managed, nil, Create},
		{"owned present -> skip", ".golangci.yml", Owned, []byte("user config"), Skip},
		{"managed unchanged-since-generated -> replace", "hooks/x.sh", Managed, []byte("v1"), Replace},
		{"managed already matches rendered -> nochange", "hooks/x.sh", Managed, []byte("v2"), NoChange},
		{"managed locally modified -> conflict", "hooks/x.sh", Managed, []byte("HAND EDITED"), Conflict},
		{"managed untracked pre-existing -> conflict", "hooks/y.sh", Managed, []byte("stranger"), Conflict},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := m.Decide(tc.rel, tc.kind, tc.onDisk, rendered); got != tc.want {
				t.Fatalf("Decide = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestMarshalDeterministic(t *testing.T) {
	m := New()
	m.Set("b.sh", Managed, []byte("x"))
	m.Set("a.sh", Managed, []byte("y"))
	b1, err := m.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	b2, _ := m.Marshal()
	if string(b1) != string(b2) {
		t.Fatal("manifest marshal not deterministic")
	}
}

func TestEmpty(t *testing.T) {
	if !New().Empty() {
		t.Fatal("new manifest should be empty")
	}
}
