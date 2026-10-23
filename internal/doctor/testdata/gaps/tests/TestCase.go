package tests

import (
	"github.com/arandu-io/framework/test/browser"
	"github.com/arandu-io/framework/test/browser/cdp"
)

// Drivers is where the drivers are named, once: not a test of one driver.
var Drivers = browser.Registry{"cdp": cdp.Launch}
