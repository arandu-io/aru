package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// hesapeModule is the module the generated entities are written against.
const hesapeModule = "github.com/arandu-io/hesape"

// modelCoreRelease is the first hesape release whose database/model is the
// non-generic core: model.Model embedded without a type argument,
// model.NewTable and the builder the generated query forwards to. A project
// pinned below it would receive an entity its hesape cannot compile.
const modelCoreRelease = "v0.47.0"

// requireModelCore refuses a project whose go.mod pins hesape below the release
// the generated entity needs, and says how to move it.
//
// It reads go.mod as text, as everything else here does. A project that does
// not require hesape at all, or replaces it with a directory, is not refused:
// the first resolves whatever `go mod tidy` picks, and the second is somebody
// working on hesape itself, whose version is the directory and not a number.
func requireModelCore(command, root string) error {
	pinned, ok := pinnedVersion(root, hesapeModule)
	if !ok || !semverLess(pinned, modelCoreRelease) {
		return nil
	}
	return fmt.Errorf(`%[1]s: this project pins %[2]s %[3]s, and the model %[1]s writes needs %[4]s or later.
The entity embeds the non-generic model.Model and declares its table with model.NewTable, which
earlier releases do not have, so the file would not compile here.

Move the project first. The upgrade tool rewrites the generic models and the code that calls them,
and names the file and line of anything it cannot rewrite; then take the release that has the core:

    go run github.com/arandu-io/aru/cmd/model-upgrade@%[5]s ./...
    go get %[2]s@%[4]s`, command, hesapeModule, pinned, modelCoreRelease, upgradeToolVersion())
}

// upgradeToolVersion is the version of the upgrade tool to name: the one this
// CLI was released as, or latest for a build from source.
func upgradeToolVersion() string {
	if strings.HasPrefix(version, "v") {
		if _, ok := parseSemver(version); ok {
			return version
		}
	}
	return "latest"
}

// pinnedVersion answers the version go.mod requires for module, after any
// replace that names a version. A replace with a directory answers false.
func pinnedVersion(root, module string) (string, bool) {
	body, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return "", false
	}

	var required, replaced string
	inRequire, inReplace := false, false
	for _, raw := range strings.Split(string(body), "\n") {
		line := strings.TrimSpace(raw)
		if i := strings.Index(line, "//"); i >= 0 {
			line = strings.TrimSpace(line[:i])
		}
		switch {
		case line == ")":
			inRequire, inReplace = false, false
			continue
		case line == "require (":
			inRequire = true
			continue
		case line == "replace (":
			inReplace = true
			continue
		}

		fields := strings.Fields(line)
		if len(fields) > 0 && (fields[0] == "require" || fields[0] == "replace") {
			kind := fields[0]
			fields = fields[1:]
			if kind == "require" {
				if v, ok := requireLine(fields, module); ok {
					required = v
				}
			} else if v, ok := replaceLine(fields, module); ok {
				replaced = v
			}
			continue
		}
		if inRequire {
			if v, ok := requireLine(fields, module); ok {
				required = v
			}
		}
		if inReplace {
			if v, ok := replaceLine(fields, module); ok {
				replaced = v
			}
		}
	}

	switch {
	case replaced == "-":
		return "", false
	case replaced != "":
		return replaced, true
	case required != "":
		return required, true
	}
	return "", false
}

func requireLine(fields []string, module string) (string, bool) {
	if len(fields) >= 2 && fields[0] == module {
		return fields[1], true
	}
	return "", false
}

// replaceLine reads "module [version] => target [version]". A target without
// a version is a directory, answered as "-".
func replaceLine(fields []string, module string) (string, bool) {
	if len(fields) < 3 || fields[0] != module {
		return "", false
	}
	arrow := -1
	for i, f := range fields {
		if f == "=>" {
			arrow = i
		}
	}
	if arrow < 0 || arrow+1 >= len(fields) {
		return "", false
	}
	if arrow+2 < len(fields) {
		return fields[arrow+2], true
	}
	return "-", true
}

// semver is a parsed version: three numbers and a pre-release, which sorts
// before the release it precedes.
type semver struct {
	nums [3]int
	pre  string
}

func parseSemver(v string) (semver, bool) {
	v, ok := strings.CutPrefix(v, "v")
	if !ok {
		return semver{}, false
	}
	if i := strings.IndexByte(v, '+'); i >= 0 {
		v = v[:i]
	}
	var out semver
	core := v
	if i := strings.IndexByte(v, '-'); i >= 0 {
		core, out.pre = v[:i], v[i+1:]
	}
	parts := strings.Split(core, ".")
	if len(parts) != 3 {
		return semver{}, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return semver{}, false
		}
		out.nums[i] = n
	}
	return out, true
}

// semverLess reports whether a sorts before b. A version that does not parse is
// not less than anything: refusing on a string this cannot read would refuse a
// project for the reader's limits rather than for its pin.
func semverLess(a, b string) bool {
	x, okA := parseSemver(a)
	y, okB := parseSemver(b)
	if !okA || !okB {
		return false
	}
	for i := range x.nums {
		if x.nums[i] != y.nums[i] {
			return x.nums[i] < y.nums[i]
		}
	}
	switch {
	case x.pre == y.pre:
		return false
	case x.pre == "":
		return false
	case y.pre == "":
		return true
	}
	return x.pre < y.pre
}
