// SPDX-License-Identifier: GPL-3.0-or-later

package runner

import (
	"strings"
	"testing"

	"github.com/Fenron-dev/reposhelf/internal/repometa"
)

// TestPythonPlatforms: packages for the chosen platforms, CPU PyTorch
// unless CUDA is on, an exact version list always (D-026).
func TestPythonPlatforms(t *testing.T) {
	base := func() eco {
		return eco{Name: "python", Fetch: "pip download -d /src/" + capsuleDir + "/wheels -r requirements.txt", Keep: []string{capsuleDir + "/wheels"}}
	}
	e := base()
	pythonPlatforms(&e, repometa.Buildable{Platforms: []string{containerPlatform, "windows/amd64", "darwin/arm64"}})
	for _, want := range []string{
		"pip download -d /src/" + capsuleDir + "/wheels --extra-index-url " + cpuIndex + " -r requirements.txt",
		"VERSIONS.txt",
		"--platform win_amd64 --extra-index-url " + cpuIndex + " -r requirements.txt",
		"--platform macosx_14_0_arm64 -r requirements.txt",
		"--only-binary=:all:",
		"Hinweis: nicht alle Pakete gibt es fertig für windows/amd64",
	} {
		if !strings.Contains(e.Fetch, want) {
			t.Errorf("fetch lacks %q:\n%s", want, e.Fetch)
		}
	}
	if strings.Count(e.Fetch, "--only-binary") != 2 {
		t.Errorf("container platform downloaded twice:\n%s", e.Fetch)
	}
	if len(e.Keep) != 3 || e.Keep[1] != capsuleDir+"/wheels-windows-amd64" {
		t.Errorf("keep = %v", e.Keep)
	}

	e = base()
	pythonPlatforms(&e, repometa.Buildable{CUDA: true})
	if strings.Contains(e.Fetch, cpuIndex) {
		t.Error("CUDA on, still the CPU index")
	}
}
