package jobs

import "testing"

// TestRetryInvoiceIsRetriedOnce is a test in a file go test will not run.
//
// The name ends in Test.go rather than _test.go, so the compiler puts this file
// in package jobs like any other source file and nothing in it is ever
// executed. There is no error and no warning: the suite is simply shorter than
// it looks, and only the count of tests executed would say so.
func TestRetryInvoiceIsRetriedOnce(t *testing.T) {
	t.Fatal("this never runs, which is the point of the fixture")
}
