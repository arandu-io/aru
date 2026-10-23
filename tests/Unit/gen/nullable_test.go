package gen_test

import (
	"strings"
	"testing"

	"github.com/arandu-io/aru/internal/gen"
)

// TestTheColumnHoldsWhatTheFieldCan: the migration, the entity and the request
// agree about which columns hold NULL. A date or a timestamp nobody is required
// to fill is a nullable column and a pointer, which is nil when it is unset; any
// other field nobody is required to fill is NOT NULL with its zero as the
// default, because its Go type has no NULL to read one into. Long text takes no
// default, which MySQL refuses on TEXT.
func TestTheColumnHoldsWhatTheFieldCan(t *testing.T) {
	m := gen.Module{
		Name: "shipment",
		Fields: []gen.Field{
			{Name: "code", Type: gen.TypeString, Required: true},
			{Name: "shipped_at", Type: gen.TypeTimestamp},
			{Name: "due_on", Type: gen.TypeDate},
			{Name: "packed_at", Type: gen.TypeTimestamp, Required: true},
			{Name: "carrier", Type: gen.TypeString},
			{Name: "weight", Type: gen.TypeDecimal},
			{Name: "fragile", Type: gen.TypeBool},
			{Name: "remarks", Type: gen.TypeText},
		},
		ModulePath: "example.test/project",
		Date:       "2026_07_31",
	}
	files := byName(t, m)
	migration := files["2026_07_31_000001_create_shipments_table.go"]
	for _, want := range []string{
		`table.String("code")` + "\n",
		`table.Timestamp("shipped_at").Nullable()`,
		`table.Date("due_on").Nullable()`,
		`table.Timestamp("packed_at")` + "\n",
		`table.String("carrier").Default("")`,
		`table.Double("weight").Default(0)`,
		`table.Boolean("fragile").Default(false)`,
		`table.Text("remarks")` + "\n",
	} {
		if !strings.Contains(migration, want) {
			t.Errorf("the migration does not declare %s:\n%s", strings.TrimSpace(want), migration)
		}
	}

	for _, file := range []string{"Shipment.go", "ShipmentRequest.go"} {
		source := strings.Join(strings.Fields(files[file]), " ")
		for _, want := range []string{"ShippedAt *time.Time", "DueOn *time.Time", "PackedAt time.Time", "Carrier string"} {
			if !strings.Contains(source, want) {
				t.Errorf("%s does not declare %s", file, want)
			}
		}
	}

	controller := files["ShipmentController.go"]
	if !strings.Contains(controller, "if sh.ShippedAt != nil {\n\t\trow.ShippedAt = sh.ShippedAt.Format(") {
		t.Errorf("the controller formats a time that may be nil without asking:\n%s", controller)
	}
}
