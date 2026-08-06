package release

import (
	"strings"
	"testing"
)

func TestParseVersion(t *testing.T) {
	tests := []struct {
		name       string
		tag        string
		wantErr    bool
		wantBranch string
		wantRC     int
		wantPatch  bool
		wantExists bool
	}{
		{
			name:       "release candidate",
			tag:        "v0.10.0-rc.1",
			wantBranch: "release-0.10",
			wantRC:     1,
			wantExists: false,
		},
		{
			name:       "minor release",
			tag:        "v0.10.0",
			wantBranch: "release-0.10",
			wantExists: true,
		},
		{
			name:       "patch release",
			tag:        "v0.9.1",
			wantBranch: "release-0.9",
			wantPatch:  true,
			wantExists: true,
		},
		{
			name:       "double digit minor keeps both digits",
			tag:        "v1.12.3",
			wantBranch: "release-1.12",
			wantPatch:  true,
			wantExists: true,
		},
		{name: "missing v prefix", tag: "0.10.0", wantErr: true},
		{name: "not semver", tag: "v0.10", wantErr: true},
		{name: "rc zero", tag: "v0.10.0-rc.0", wantErr: true},
		{name: "trailing junk", tag: "v0.10.0-rc.1-dirty", wantErr: true},
		{name: "empty", tag: "", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v, err := ParseVersion(tt.tag)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("ParseVersion(%q) = nil error, want error", tt.tag)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseVersion(%q): %v", tt.tag, err)
			}
			if v.Branch != tt.wantBranch {
				t.Errorf("Branch = %q, want %q", v.Branch, tt.wantBranch)
			}
			if v.RC != tt.wantRC {
				t.Errorf("RC = %d, want %d", v.RC, tt.wantRC)
			}
			if v.IsPatch() != tt.wantPatch {
				t.Errorf("IsPatch() = %v, want %v", v.IsPatch(), tt.wantPatch)
			}
			if v.ExpectsExistingBranch() != tt.wantExists {
				t.Errorf("ExpectsExistingBranch() = %v, want %v", v.ExpectsExistingBranch(), tt.wantExists)
			}
		})
	}
}

// The whole point of deriving from one argument is that the branch and
// the version cannot disagree the way two exported variables can.
func TestEnvIsInternallyConsistent(t *testing.T) {
	v, err := ParseVersion("v0.10.0-rc.1")
	if err != nil {
		t.Fatalf("ParseVersion: %v", err)
	}
	env := v.Env("upstream", v.Tag)

	got := make(map[string]string, len(env))
	for _, e := range env {
		k, val, ok := strings.Cut(e, "=")
		if !ok {
			t.Fatalf("malformed env entry %q", e)
		}
		got[k] = val
	}

	if want := "release-" + got["BRANCH_VERSION"]; got["RELEASE_BRANCH"] != want {
		t.Errorf("RELEASE_BRANCH = %q, want %q", got["RELEASE_BRANCH"], want)
	}
	if !strings.HasPrefix(got["VERSION"], "v"+got["BRANCH_VERSION"]+".") {
		t.Errorf("VERSION %q does not belong to BRANCH_VERSION %q", got["VERSION"], got["BRANCH_VERSION"])
	}
}

func TestImageNameParsing(t *testing.T) {
	// Shape of the set-params step in ci-release.yaml.
	const yaml = `
      - name: Set image names
        id: version
        run: |
          repo="${GITHUB_REPOSITORY##*/}"
          echo "epp_name=${repo}-endpoint-picker" >> "$GITHUB_OUTPUT"
          echo "sidecar_name=${repo}-disagg-sidecar" >> "$GITHUB_OUTPUT"
          echo "coordinator_name=${repo}-coordinator" >> "$GITHUB_OUTPUT"
`
	var got []string
	for _, m := range imageNameRE.FindAllStringSubmatch(yaml, -1) {
		got = append(got, "llm-d-router-"+m[2])
	}
	got = dedupe(got)

	want := []string{
		"llm-d-router-coordinator",
		"llm-d-router-disagg-sidecar",
		"llm-d-router-endpoint-picker",
	}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("image[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestChartNameParsing(t *testing.T) {
	const yaml = `
      - name: Build and Push Standalone Chart
        uses: ./.github/actions/helm-build-and-push
        with:
          chart_name: llm-d-router-standalone
      - name: Build and Push Gateway Chart
        uses: ./.github/actions/helm-build-and-push
        with:
          chart_name: llm-d-router-gateway
`
	var got []string
	for _, m := range chartNameRE.FindAllStringSubmatch(yaml, -1) {
		got = append(got, m[1])
	}
	got = dedupe(got)

	want := []string{"llm-d-router-gateway", "llm-d-router-standalone"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("chart[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestPushStepsAreNeverLocal(t *testing.T) {
	v, err := ParseVersion("v0.10.0-rc.1")
	if err != nil {
		t.Fatalf("ParseVersion: %v", err)
	}
	p := &Plan{
		Repo: "llm-d-router", RepoDir: "/repo", Worktree: "/wt",
		Version: v, Remote: "upstream", Predictor: v.Tag,
	}
	p.Steps = buildSteps(p, false)

	for i, s := range p.Steps {
		isPush := len(s.Cmd) > 1 && s.Cmd[1] == "push"
		if isPush && !s.Remote {
			t.Errorf("step %d pushes but is not marked Remote: %s", i+1, s.String())
		}
		if s.Remote && !isPush {
			t.Errorf("step %d marked Remote but does not push: %s", i+1, s.String())
		}
	}
}

func TestPredictorPinSkippedWhenAligned(t *testing.T) {
	v, err := ParseVersion("v0.10.0")
	if err != nil {
		t.Fatalf("ParseVersion: %v", err)
	}

	aligned := &Plan{Repo: "r", Version: v, Remote: "upstream", Predictor: v.Tag}
	aligned.Steps = buildSteps(aligned, true)
	if !findPin(t, aligned).Skip {
		t.Error("predictor pin should be skipped when it tracks the release version")
	}

	override := &Plan{Repo: "r", Version: v, Remote: "upstream", Predictor: "v0.8.0"}
	override.Steps = buildSteps(override, true)
	if findPin(t, override).Skip {
		t.Error("predictor pin should run when overridden")
	}
}

func findPin(t *testing.T, p *Plan) Step {
	t.Helper()
	for _, s := range p.Steps {
		if strings.Contains(s.Desc, "LATENCY_PREDICTOR_TAG") {
			return s
		}
	}
	t.Fatal("no predictor pin step in plan")
	return Step{}
}
