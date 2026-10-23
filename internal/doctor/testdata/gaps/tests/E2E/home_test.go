//go:build e2e

package e2e_test

import (
	"testing"

	"github.com/arandu-io/framework/test/browser"

	"example.test/gaps/tests"
)

// A journey in the user's words, with the one CSS locator kept for the
// reason the kit asks for.
func TestSmokeHome(t *testing.T) {
	page := tests.Open(t, tests.Serve(t))
	page.Visit("home")
	page.ClickRole("button", "Pay")
	page.Fill("#tag", "urgent")
	page.Click(browser.CSS("canvas.chart", "the chart has no accessible name"))
}
