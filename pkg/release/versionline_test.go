package release

import "testing"

func mustVersion(t *testing.T, tag string) *Version {
	t.Helper()
	v, err := ParseVersion(tag)
	if err != nil {
		t.Fatalf("ParseVersion(%q): %v", tag, err)
	}
	return v
}

func TestCompare(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{"v0.9.0", "v0.8.1", 1},
		{"v0.8.1", "v0.9.0", -1},
		{"v0.9.0", "v0.9.0", 0},
		{"v0.10.0", "v0.9.0", 1},     // numeric, not lexical
		{"v0.9.0", "v0.10.0", -1},    // the ordering a string sort gets wrong
		{"v0.9.0", "v0.9.0-rc.1", 1}, // final beats its own rc
		{"v0.9.0-rc.2", "v0.9.0-rc.1", 1},
		{"v1.0.0", "v0.99.99", 1},
	}

	for _, tt := range tests {
		got := Compare(mustVersion(t, tt.a), mustVersion(t, tt.b))
		if got != tt.want {
			t.Errorf("Compare(%s, %s) = %d, want %d", tt.a, tt.b, got, tt.want)
		}
	}
}

// llm-d and llm-d-router advance independently. A version legal on one
// line can be one the other already shipped, which is the mistake this
// guards.
func TestValidateSuccessor(t *testing.T) {
	tests := []struct {
		name        string
		latest      string
		want        string
		wantErr     bool
		wantSkipped bool
	}{
		{name: "next patch", latest: "v0.9.0", want: "v0.9.1"},
		{name: "next minor", latest: "v0.9.0", want: "v0.10.0"},
		{name: "next major", latest: "v0.9.0", want: "v1.0.0"},
		{name: "rc of next minor", latest: "v0.9.0", want: "v0.10.0-rc.1"},
		{name: "second rc of in-progress version", latest: "v0.10.0-rc.1", want: "v0.10.0-rc.2"},
		{name: "final after its rc", latest: "v0.10.0-rc.2", want: "v0.10.0"},

		{name: "already released", latest: "v0.9.0", want: "v0.9.0", wantErr: true},
		{name: "older", latest: "v0.9.0", want: "v0.8.1", wantErr: true},
		{name: "older rc", latest: "v0.10.0", want: "v0.10.0-rc.1", wantErr: true},

		// The umbrella repo is at v0.8.1 while the router is at v0.9.0.
		// Cutting the router's next version on llm-d skips a minor.
		{name: "router version on the umbrella line", latest: "v0.8.1", want: "v0.10.0", wantSkipped: true},
		{name: "skips two minors", latest: "v0.9.0", want: "v0.12.0", wantSkipped: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status, detail := ValidateSuccessor(mustVersion(t, tt.latest), mustVersion(t, tt.want))
			if tt.wantErr {
				if status != Fail {
					t.Fatalf("ValidateSuccessor(%s, %s) = %v, want Fail", tt.latest, tt.want, status)
				}
				return
			}
			want := Pass
			if tt.wantSkipped {
				want = Warn
			}
			if status != want {
				t.Errorf("status = %v (%q), want %v", status, detail, want)
			}
		})
	}
}

func TestValidateSuccessorNoPriorRelease(t *testing.T) {
	status, detail := ValidateSuccessor(nil, mustVersion(t, "v0.1.0"))
	if status != Pass {
		t.Fatalf("first release should be allowed, got %v (%q)", status, detail)
	}
}

func TestChecksFailedAndWarnings(t *testing.T) {
	c := Checks{
		{Name: "a", Status: Pass},
		{Name: "b", Status: Warn, Detail: "careful"},
	}
	if c.Failed() {
		t.Error("Failed() = true with no failing check")
	}
	if got := c.NonPassing(); len(got) != 1 || got[0] != "careful" {
		t.Errorf("NonPassing() = %v, want [careful]", got)
	}

	c = append(c, Check{Name: "c", Status: Fail, Detail: "stop"})
	if !c.Failed() {
		t.Error("Failed() = false with a failing check")
	}
	if got := c.NonPassing(); len(got) != 2 {
		t.Errorf("NonPassing() = %v, want 2 entries", got)
	}

	// A check that never set its status must not read as success.
	if !(Checks{{Name: "never ran"}}).Failed() {
		t.Error("Failed() = false for a check with the zero-value status")
	}
}
