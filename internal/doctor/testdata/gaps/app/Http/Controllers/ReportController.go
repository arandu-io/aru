package controllers

import (
	"net/http"

	hhttp "github.com/arandu-io/hesape/http"

	requests "example.test/gaps/app/Http/Requests"
	services "example.test/gaps/app/Services"
)

// ReportController is the shape the structural rules must stay quiet on: every
// action below looks like one of their findings and is not.
type ReportController struct {
	reports *services.ExportService
	cache   interface {
		Load(key string) (string, bool)
	}
}

// NewReportController is constructed in bootstrap, so it is wired.
func NewReportController(reports *services.ExportService) *ReportController {
	return &ReportController{reports: reports}
}

// Store binds the form, and a Load on a cache is not a session.
func (c *ReportController) Store(ctx *hhttp.Context) error {
	var in requests.StoreReport
	if err := ctx.Bind(&in); err != nil {
		return err
	}
	if _, warm := c.cache.Load("reports"); warm {
		return ctx.RedirectRoute("reports.index")
	}
	return ctx.RedirectRoute("reports.index")
}

// Back compares a status without answering one, and redirects to a value and
// to another site, neither of which is a path of this application.
func (c *ReportController) Back(ctx *hhttp.Context) error {
	status := http.StatusOK
	if status == http.StatusUnprocessableEntity {
		return nil
	}
	if ctx.Query("external") != "" {
		return ctx.Redirect("https://example.com/reports")
	}
	next := ctx.URL("reports.index")
	return ctx.Redirect(next)
}

// Kind switches on a route parameter, which names the route rather than an
// operation a hidden input picks.
func (c *ReportController) Kind(ctx *hhttp.Context) error {
	switch ctx.Param("kind") {
	case "monthly":
		return c.reports.Monthly(ctx.Ctx(), nil)
	case "export":
		return c.reports.Export(ctx.Ctx(), nil)
	}
	return nil
}

// Download streams a file: its status is written, and no JSON is.
func (c *ReportController) Download(ctx *hhttp.Context) error {
	ctx.Response.Header().Set("Content-Type", "text/csv")
	ctx.Response.WriteHeader(http.StatusOK)
	_, err := ctx.Response.Write([]byte("id\n"))
	return err
}

// Row answers a fragment from resources/views/partials.
func (c *ReportController) Row(ctx *hhttp.Context) error {
	return ctx.Fragment(http.StatusOK, "partials.report-row", services.ReportRow{Title: "Monthly"})
}
