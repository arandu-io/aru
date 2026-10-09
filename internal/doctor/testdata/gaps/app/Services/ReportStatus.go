package services

import (
	"net/http"

	"github.com/arandu-io/hesape/auth"
)

// ReportRow is what the listing renders for one report.
type ReportRow struct {
	Title string
}

// ReportGoneError says its status through net/http, which is how a domain error
// tells the router what to answer.
type ReportGoneError struct{}

func (ReportGoneError) Error() string { return "report: gone" }

// HTTPStatus is the status the router maps this error to.
func (ReportGoneError) HTTPStatus() int { return http.StatusGone }

// contentTypeOf sniffs bytes, which reads no request and writes no response.
func contentTypeOf(body []byte) string { return http.DetectContentType(body) }

// subjectOf carries the roles that were stored for the account, unchanged.
func subjectOf(id, tenant string, stored []string) auth.Subject {
	return auth.Subject{ID: id, Tenant: tenant, Roles: append([]string(nil), stored...)}
}

// anonymous carries no roles and no actions at all.
func anonymous(tenant string) auth.Subject {
	return auth.Subject{ID: "", Tenant: tenant}
}
