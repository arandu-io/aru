package gen_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/arandu-io/aru/internal/gen"
)

// The tests here each lock one defect an audit found in the generated code.
// They read the generated source rather than run it, because what went wrong
// was what got written -- and a golden file records the whole output without
// saying which line is the one that matters.

// TestTheListingPagesThroughTheModel: the listing used to page by hand -- a
// keyset predicate, a sort allowlist and a limit clamp, thirty lines per module
// -- and it once compared created_at while ordering by whatever q.Sort named, so
// pages skipped rows and repeated rows, silently and with no error.
//
// The page is the Model's now, and so is what the request may ask of it: the
// service declares the listing once -- the columns searched and ordered by, the
// page sizes, the default order and its tie-break on the key -- the controller
// reads the query string through it, and the Model's Listing runs the page, the
// total and the facet counts under the Grant. Nothing from the request names a
// column, a direction or a size.
func TestTheListingPagesThroughTheModel(t *testing.T) {
	for _, tenant := range []bool{true, false} {
		files := byName(t, spec(tenant))
		service := files["PurchaseOrderService.go"]
		// gofmt aligns the values of a literal, so the spacing is not the claim.
		flat := strings.Join(strings.Fields(service), " ")

		for _, want := range []string{
			"pagination.NewListing(pagination.ListingSpec{",
			`DefaultSort: "created_at"`,
			"DefaultDir: pagination.SortDesc",
			`TieBreak: "id"`,
			"models.PurchaseOrders(s.db).Listing(ctx, g, state, path)",
			"func (s *PurchaseOrderService) Listing() *pagination.Listing",
		} {
			if !strings.Contains(flat, want) {
				t.Errorf("tenant=%v: the service does not declare and run the listing (no %q)", tenant, want)
			}
		}
		for _, handRolled := range []string{"q.Sort", "q.Cursor", "Sortable", "after.Where(", "SimplePaginate", "OrderBy(", "PerPage ="} {
			if strings.Contains(service, handRolled) {
				t.Errorf("tenant=%v: the service still pages or orders by hand: %s", tenant, handRolled)
			}
		}

		controller := files["PurchaseOrderController.go"]
		if !strings.Contains(controller, "c.svc.Listing().ParseURL(ctx.Request.URL)") {
			t.Errorf("tenant=%v: the controller does not read the query string through the service's listing", tenant)
		}
		for _, raw := range []string{`ctx.Query("sort")`, `ctx.Query("limit")`, `ctx.Query("per_page")`, `ctx.Query("dir")`, "ResolveCurrentPage"} {
			if strings.Contains(controller, raw) {
				t.Errorf("tenant=%v: the controller hands a request value to the listing's order or size: %s", tenant, raw)
			}
		}
	}
}

// TestTheListingColumnsAreTheSameInTheServiceAndTheView: the table's column keys
// are the public keys the service's listing declares, for the sort and for the
// columns that can be hidden. A key the listing does not know changes nothing --
// the sort arrow would draw and do nothing -- so the two are compared here, from
// the same field list.
func TestTheListingColumnsAreTheSameInTheServiceAndTheView(t *testing.T) {
	files := byName(t, spec(true))
	service, view := files["PurchaseOrderService.go"], files["index.kyse.go"]
	flat := strings.Join(strings.Fields(service), " ")

	for _, column := range []string{"reference", "supplier_email", "total", "approved", "notes", "delivery_date", "created_at"} {
		if !strings.Contains(flat, `"`+column+`": "`+column+`",`) {
			t.Errorf("the service's listing does not declare a sort for %q", column)
		}
		if !strings.Contains(view, `Key: "`+column+`", Sortable: true`) {
			t.Errorf("the index view has no sortable column for %q", column)
		}
	}
	// Searched: the columns that hold text, and only those.
	searched := service[strings.Index(service, "Search: []string{"):]
	searched = searched[:strings.Index(searched, "},")]
	for _, column := range []string{"reference", "supplier_email", "notes"} {
		if !strings.Contains(searched, `"`+column+`"`) {
			t.Errorf("the listing does not search %q, which holds text", column)
		}
	}
	for _, column := range []string{"total", "approved", "delivery_date"} {
		if strings.Contains(searched, `"`+column+`"`) {
			t.Errorf("the listing searches %q, which does not hold text", column)
		}
	}
	// The first column is the link to the record and is not hideable.
	hideable := service[strings.Index(service, "Hideable: []string{"):]
	hideable = hideable[:strings.Index(hideable, "},")]
	if strings.Contains(hideable, `"reference"`) {
		t.Error("the listing lets the column that links to the record be hidden")
	}
	for _, other := range []string{"total", "created_at"} {
		if !strings.Contains(hideable, `"`+other+`"`) {
			t.Errorf("the listing does not let %q be hidden", other)
		}
	}

	// One pager: the table's own. A second one beside it is two sources of the
	// page being shown.
	for name, body := range files {
		if strings.HasSuffix(name, ".kyse.go") && strings.Contains(body, "components.Pagination(") {
			t.Errorf("%s draws a pager of its own beside the table's", name)
		}
	}
}

// TestListingIsItsOwnPermission: ActionList was declared, granted in the
// specification, and never checked. List authorized ActionView, so whoever
// could open one record could page through every record there is.
func TestListingIsItsOwnPermission(t *testing.T) {
	files := byName(t, spec(true))

	service := files["PurchaseOrderService.go"]
	if !strings.Contains(service, "PurchaseOrderList") {
		t.Error("Service.List does not authorize the list permission")
	}
}

// TestARequiredNumberCanBeFilledIn: Required took a string, and the generator
// handed it a literal "" for every non-text field, so a required int, date,
// amount or decimal failed validation with "is required" no matter what was
// sent and the generated create endpoint could not be used at all. The rules are
// strings now, and what has to hold is that each one says what the column holds,
// so that a number is checked as a number and text as text.
func TestARequiredNumberCanBeFilledIn(t *testing.T) {
	request := byName(t, spec(true))["PurchaseOrderRequest.go"]

	if !strings.Contains(request, `"required|integer",`) {
		t.Errorf("a required amount is not declared required and integer:\n%s", request)
	}
	if !strings.Contains(request, `"required|string|max:255",`) {
		t.Errorf("required text is not declared required, string and capped:\n%s", request)
	}
}

// TestDecimalKeepsItsDigits: REAL is four bytes in PostgreSQL, so a float64
// written through it came back rounded to about seven digits -- on read,
// silently, with no error anywhere.
func TestDecimalKeepsItsDigits(t *testing.T) {
	m := spec(true)
	m.Fields = append(m.Fields, gen.Field{Name: "rate", Type: gen.TypeDecimal})

	// The migration no longer writes the type: it names a Blueprint method and
	// the grammar writes the type per engine. So what this test can still assert
	// here is the method, and what it used to assert -- that the column is eight
	// bytes and not four -- is now a property of the Postgres grammar, tested
	// where the grammar lives.
	migration := collapse(migrationOf(t, m))
	if !strings.Contains(migration, `table.Double("rate")`) {
		t.Errorf("decimal does not declare Double:\n%s", migration)
	}
	if strings.Contains(migration, `table.Float("rate")`) {
		t.Fatal("decimal declares Float, which is four bytes in PostgreSQL and rounds a float64 on read")
	}
}

// TestNoGeneratedFileWritesAnAddress: the controller redirected to
// "/purchase-orders/"+id and the four views wrote the same prefix into every
// href, form action and hx- attribute. Both compile, both render, and both keep
// pointing at the old address the moment the route moves -- there is no build
// failure and no error at request time, so the first report is somebody
// clicking a link that 404s.
//
// Every one of them is now asked for by route name, which fails where the name
// is wrong and says which name. The check is that no generated file writes a
// path at all: an address that appears once is an address the next regeneration
// keeps.
//
// It reads every file the module emits, prose included. The skill file used to
// name the prefix in its description, and a description that goes stale is a
// smaller failure than a dead link but it is the same failure -- so it says
// "purchase-orders routes" and nothing here needs an exception.
func TestNoGeneratedFileWritesAnAddress(t *testing.T) {
	for _, tenant := range []bool{true, false} {
		for name, body := range byName(t, spec(tenant)) {
			for _, line := range strings.Split(body, "\n") {
				// The views package is imported by a path that begins with the
				// module path and carries the resource as its last segment. It
				// is an import, not an address, and it is the one place the
				// resource legitimately appears inside a quoted string.
				if strings.Contains(line, `views "`) || strings.Contains(line, "/storage/framework/views/") {
					continue
				}
				if strings.Contains(line, "/purchase-orders") || strings.Contains(line, "/auth/login") {
					t.Errorf("tenant=%v: %s writes an address instead of asking for a route by name:\n\t%s",
						tenant, name, strings.TrimSpace(line))
				}
			}
		}
	}
}

// TestTheGeneratedControllerAsksForEveryAddressByItsRouteName is the other half
// of the check above: nothing writes a path, and what replaced it is the seven
// names Resource registers for this module.
//
// The names are written out rather than derived, because deriving them from the
// same method the generator uses would make this test agree with the generator
// about a convention neither of them owns. The convention is the router's, and
// these are the names it gives.
func TestTheGeneratedControllerAsksForEveryAddressByItsRouteName(t *testing.T) {
	for _, tenant := range []bool{true, false} {
		files := byName(t, spec(tenant))
		controller := files["PurchaseOrderController.go"]

		for _, want := range []string{
			`ctx.URL("purchase-orders.index")`,
			`ctx.URL("purchase-orders.create")`,
			`ctx.URL("purchase-orders.store")`,
			`ctx.URL("purchase-orders.show", p.ID)`,
			`ctx.URL("purchase-orders.edit", found.ID)`,
			`ctx.URL("purchase-orders.update", found.ID)`,
			`ctx.URL("purchase-orders.destroy", found.ID)`,
			`ctx.RedirectRoute("purchase-orders.show", created.ID)`,
			`ctx.RedirectRoute("purchase-orders.show", updated.ID)`,
			`ctx.RedirectRoute("purchase-orders.index")`,
		} {
			if !strings.Contains(controller, want) {
				t.Errorf("tenant=%v: the controller does not ask for %s", tenant, want)
			}
		}
	}
}

// byName generates a module and returns its files keyed by base name.
func byName(t *testing.T, m gen.Module) map[string]string {
	t.Helper()
	files, err := gen.Generate(m)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	out := map[string]string{}
	for _, f := range files {
		out[filepath.Base(f.Path)] = string(f.Content)
	}
	return out
}

// collapse squeezes runs of whitespace to one space, so a test about content
// does not fail on alignment.
func collapse(s string) string { return strings.Join(strings.Fields(s), " ") }

// TestNothingKeyedIsDeclaredTEXT is the defect that made MySQL unusable, and it
// is checked by shape rather than by example because it repeated in five places.
//
// MySQL stores TEXT off-page and refuses it in a key without a prefix length, so
// `id TEXT PRIMARY KEY` fails with "BLOB/TEXT column used in key specification
// without a key length". That is the first statement of the first migration --
// MySQL never gets past creating the table.
func TestNothingKeyedIsDeclaredTEXT(t *testing.T) {
	m := spec(true)
	migration := collapse(migrationOf(t, m))

	// The generator stopped choosing between VARCHAR and TEXT, and that is the
	// fix rather than a loss of coverage: the grammar makes that choice per
	// engine, which is why it exists. What is still the generator's to get right
	// is which Blueprint method a keyed column gets, because Text is the one
	// MySQL refuses in a key.
	for _, keyed := range []string{"id", "tenant_id", "reference"} {
		if strings.Contains(migration, `table.Text("`+keyed+`")`) {
			t.Errorf("the keyed column %q declares Text, which MySQL refuses in a key without a prefix length", keyed)
		}
		if !strings.Contains(migration, `table.String("`+keyed+`")`) {
			t.Errorf("the keyed column %q does not declare String:\n%s", keyed, migration)
		}
	}

	// The long kind stays Text: it is never indexed, and capping it would make
	// "long text" mean 255 characters.
	if !strings.Contains(migration, `table.Text("notes")`) {
		t.Error("a text field no longer declares Text")
	}
}

// TestValidationAgreesWithTheColumn: a value that passes validation has to fit
// the column, or the rejection arrives from the database driver and names a
// column rather than the field somebody filled in.
func TestValidationAgreesWithTheColumn(t *testing.T) {
	request := byName(t, spec(true))["PurchaseOrderRequest.go"]

	// reference is a string: VARCHAR(255), so 255.
	if !strings.Contains(request, `"required|string|max:255"`) {
		t.Error("a string field is not capped at the width of its column")
	}
	// supplier_email is an address: the addr-spec limit, under the column.
	if !strings.Contains(request, `"required|email|max:254"`) {
		t.Error("an email field is not capped at 254")
	}
	// notes is long text: TEXT, so the cap is the generous one.
	if !strings.Contains(request, `"string|max:5000"`) {
		t.Error("a text field is capped as if it were a string")
	}
}

// migrationOf returns the generated migration, whatever the file is called.
//
// The name carries the date, and pinning the test to it breaks the test at
// midnight.
func migrationOf(t *testing.T, m gen.Module) string {
	t.Helper()
	for name, body := range byName(t, m) {
		if strings.Contains(name, "create_") && strings.HasSuffix(name, "_table.go") {
			return body
		}
	}
	t.Fatal("nenhuma migration foi gerada")
	return ""
}
