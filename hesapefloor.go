package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/arandu-io/aru/internal/gen"
	"github.com/arandu-io/aru/internal/gomod"
)

// hesapeModule is the module the generated entities are written against.
const hesapeModule = "github.com/arandu-io/hesape"

// requireModelCore refuses a project whose go.mod pins hesape below the release
// the generated entity needs, gen.ModelCoreRelease, and says how to move it: to
// gen.HesapeRelease, the release the generated code is compiled against.
//
// A project that does not require hesape at all, or replaces it with a
// directory, is not refused: the first resolves whatever `go mod tidy` picks,
// and the second is somebody working on hesape itself, whose version is the
// directory and not a number.
func requireModelCore(command, root string) error {
	body, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return nil
	}
	pinned, ok := gomod.Parse(string(body)).Pinned(hesapeModule)
	if !ok || !gomod.Less(pinned, gen.ModelCoreRelease) {
		return nil
	}
	return fmt.Errorf(`%[1]s: this project pins %[2]s %[3]s, and the model %[1]s writes needs %[4]s or later.
The entity embeds the non-generic model.Model and declares its table with model.NewTable, which
earlier releases do not have, so the file would not compile here.

Move the project first. The upgrade tool rewrites the generic models and the code that calls them,
and names the file and line of anything it cannot rewrite; then take the release that has the core:

    go run github.com/arandu-io/aru/cmd/model-upgrade@%[5]s ./...
    go get %[2]s@%[6]s`, command, hesapeModule, pinned, gen.ModelCoreRelease, upgradeToolVersion(), gen.HesapeRelease)
}

// upgradeToolVersion is the version of the upgrade tool to name: the one this
// CLI was released as, or latest for a build from source.
func upgradeToolVersion() string {
	if strings.HasPrefix(version, "v") && gomod.IsVersion(version) {
		return version
	}
	return "latest"
}
