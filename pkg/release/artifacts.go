package release

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
)

const (
	releaseWorkflow = ".github/workflows/ci-release.yaml"
	buildWorkflow   = ".github/workflows/ci-build-images.yaml"
)

// imageNameRE matches the shell assignments the release workflow uses to
// derive image names, e.g. epp_name=${repo}-endpoint-picker.
var imageNameRE = regexp.MustCompile(`(?m)^\s*echo\s+"?(\w+)_name=\$\{repo\}-([a-zA-Z0-9-]+)`)

// chartNameRE matches chart_name inputs passed to the chart push action.
var chartNameRE = regexp.MustCompile(`(?m)^\s*chart_name:\s*(\S+)`)

// Artifacts is what a release tag publishes, read from the
// workflows rather than from a hand-maintained list. Drift shows up here
// instead of accumulating silently in the issue template.
type Artifacts struct {
	Images []string
	Charts []string
}

// Empty reports whether nothing was discovered.
func (a *Artifacts) Empty() bool { return len(a.Images) == 0 && len(a.Charts) == 0 }

// EnumerateArtifacts reads the release and build workflows at ref and
// returns the images and charts the tag will publish. Reading the blob at
// ref rather than the working tree is deliberate: a checkout parked on a
// feature branch reports the wrong pipeline.
func EnumerateArtifacts(repoDir, repoName, ref string) (*Artifacts, error) {
	a := &Artifacts{}

	relYAML, err := showFile(repoDir, ref, releaseWorkflow)
	if err != nil {
		return nil, fmt.Errorf("reading %s at %s: %w", releaseWorkflow, ref, err)
	}
	for _, m := range imageNameRE.FindAllStringSubmatch(relYAML, -1) {
		a.Images = append(a.Images, repoName+"-"+m[2])
	}

	// Charts are pushed by the reusable build workflow, not the release
	// workflow that calls it.
	buildYAML, err := showFile(repoDir, ref, buildWorkflow)
	if err == nil {
		for _, m := range chartNameRE.FindAllStringSubmatch(buildYAML, -1) {
			a.Charts = append(a.Charts, m[1])
		}
	}

	a.Images = dedupe(a.Images)
	a.Charts = dedupe(a.Charts)
	return a, nil
}

// dedupe sorts and de-duplicates in place, so it consumes its argument.
func dedupe(in []string) []string {
	slices.Sort(in)
	return slices.Compact(in)
}

// Render returns the artifacts as a reviewable listing.
func (a *Artifacts) Render(registry, tag string) string {
	var b strings.Builder
	b.WriteString("Images\n")
	if len(a.Images) == 0 {
		b.WriteString("  (none discovered)\n")
	}
	for _, img := range a.Images {
		fmt.Fprintf(&b, "  %s/%s:%s\n", registry, img, tag)
	}
	b.WriteString("\nHelm charts\n")
	if len(a.Charts) == 0 {
		b.WriteString("  (none discovered)\n")
	}
	for _, c := range a.Charts {
		fmt.Fprintf(&b, "  oci://%s/charts/%s (version %s)\n", registry, c, tag)
	}
	return b.String()
}
