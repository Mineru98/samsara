package main

import (
	"os"

	"github.com/Mineru98/samsara/runtime/internal/backend"
)

// launchSpec builds the harmless doctor --exercise worker: a sleeping shell
// process in the temp dir, no Claude, no project files touched.
func launchSpec(correlation string) backend.LaunchSpec {
	return backend.LaunchSpec{Dir: os.TempDir(), Argv: []string{"sleep", "30"}, Correlation: correlation}
}
