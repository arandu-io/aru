//go:build e2e

package e2e_test

import (
	"testing"

	"github.com/arandu-io/framework/test/browser"
	"github.com/arandu-io/framework/test/browser/cdp"
	"github.com/chromedp/chromedp"

	"example.test/p/tests"
)

// The journey of a harness written before the kit: a selector for every
// element, one driver's API, and no pipeline that runs it.
func TestCheckout(t *testing.T) {
	page := tests.Open(t, tests.Serve(t))
	page.Click(browser.CSS(".pay-button", ""))
	var total string
	page.Evaluate(`document.querySelector('.total').textContent`, &total)
	harness := tests.Harness(t)
	harness.Click("#confirm")
	_ = chromedp.Navigate
	_ = page.Driver().(*cdp.Driver)
}
