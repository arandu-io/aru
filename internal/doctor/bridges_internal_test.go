package doctor

import (
	"testing"

	"github.com/arandu-io/aru/internal/catalog"
)

// TestEveryBridgeIsMapped reads the bridges back out of the framework's own
// package documentation and compares them with bridgeTargets.
//
// The table is written by hand, because the doctor's detectors ask about a
// package and the framework is not on every machine that runs them. A bridge
// added to the framework and missing here is a package every detector that
// asks bridgeTargets goes blind on; a target that moved is a detector matching
// a package nobody imports any more. The sentence each bridge carries -- what
// to import instead -- is the one statement both sides agree to keep, and the
// import catalog is what reads it, so this test and `aru imports:catalog` read
// one sentence one way.
func TestEveryBridgeIsMapped(t *testing.T) {
	root, checkedOut := siblingCheckout("framework")
	if !checkedOut {
		t.Skip("framework is not checked out next to this repository")
	}
	c, err := catalog.Read(root)
	if err != nil {
		t.Fatal(err)
	}

	declared := map[string]string{}
	for _, pkg := range c.Packages {
		if pkg.Bridge != "" {
			declared[pkg.Path] = pkg.Bridge
		}
	}
	if len(declared) == 0 {
		t.Fatal("no bridge was found in the framework: this test stopped testing anything")
	}

	for bridge, target := range declared {
		switch mapped, listed := bridgeTargets[bridge]; {
		case !listed:
			t.Errorf("%s is a bridge to %s and bridgeTargets does not list it: every detector that "+
				"accepts a bridge goes blind on a project importing it", bridge, target)
		case mapped != target:
			t.Errorf("bridgeTargets maps %s to %s, and its documentation says to import %s", bridge, mapped, target)
		}
	}
	for bridge := range bridgeTargets {
		if _, found := declared[bridge]; !found {
			t.Errorf("bridgeTargets lists %s, which the framework no longer declares a bridge", bridge)
		}
	}
}

// TestABridgeCountsAsTheComponentItPointsAt pins nativeComponent on the three
// kinds of import it is asked about.
func TestABridgeCountsAsTheComponentItPointsAt(t *testing.T) {
	for path, want := range map[string]string{
		"github.com/arandu-io/hesape/database/model": "database",
		"github.com/arandu-io/framework/data":        "database",
		"github.com/arandu-io/framework/scheduler":   "console",
		"github.com/arandu-io/framework/kernel":      "",
		"github.com/arandu-io/framework/foundation":  "",
		"github.com/arandu-io/hesape":                "",
		"example.org/hesape/database":                "",
	} {
		got, native := nativeComponent(path)
		if got != want || native != (want != "") {
			t.Errorf("nativeComponent(%q) = %q, %v; want %q", path, got, native, want)
		}
	}
	if reaches("", "github.com/arandu-io/framework/unknown") {
		t.Error("the empty path reaches a bridge nobody listed")
	}
}
