package spec

import (
	"encoding/json"
	"fmt"
	"sort"
)

// Schema returns the JSON Schema of a module specification.
//
// This is the real product of this layer. A model with no fine-tuning, given
// only the schema, writes a valid spec -- and a wrong one fails validation
// rather than becoming broken Go. That is the whole difference between this and
// asking a model to write code for a framework it has never seen.
//
// It is generated from the same constants the validator uses, so the schema and
// the checks cannot disagree. A schema published as a hand-written file drifts
// from the code on the second change.
func Schema() ([]byte, error) {
	types := make([]string, 0, len(Types))
	for name := range Types {
		types = append(types, name)
	}
	sort.Strings(types)

	// Documented inline, because the schema is what a model reads and every
	// description is a chance to prevent one wrong guess.
	typeDoc := "The type of the column. " + describeTypes()

	schema := map[string]any{
		"$schema":     "https://json-schema.org/draft/2020-12/schema",
		"$id":         "https://raw.githubusercontent.com/arandu-io/aru/main/schema/module.schema.json",
		"title":       "Arandu module",
		"description": "One module of an Arandu application: an entity, its fields, and who may do what to it. A deterministic generator turns this into Go -- the model never writes Go.",
		"type":        "object",
		"required":    []string{"version", "name", "fields"},
		// The generator refuses what it does not know, and so does the schema:
		// a typo is an error in both, at the same place, with the same reason.
		"additionalProperties": false,
		"properties": map[string]any{
			"version": map[string]any{
				"type":        "string",
				"const":       Version,
				"description": "The schema version this document is written against.",
			},
			"name": map[string]any{
				"type":        "string",
				"pattern":     "^[a-z][a-z0-9_]*[a-z0-9]$",
				"description": "The module name, lowercase with underscores: purchase_order, not PurchaseOrder.",
			},
			"description": map[string]any{
				"type":        "string",
				"description": "What this module is for, in a sentence. Not used for generation -- it is there so a person can approve the spec instead of the diff.",
			},
			"tenant": map[string]any{
				"type":        "boolean",
				"description": "Scope every query by the tenant of the Grant. True for anything that belongs to a customer, which is almost everything in a SaaS.",
			},
			"fields": map[string]any{
				"type":        "array",
				"minItems":    1,
				"description": "The columns. id, tenant_id, created_at and updated_at are generated for every module -- do not declare them.",
				"items": map[string]any{
					"type":                 "object",
					"required":             []string{"name", "type"},
					"additionalProperties": false,
					"properties": map[string]any{
						"name": map[string]any{
							"type":        "string",
							"pattern":     "^[a-z][a-z0-9_]*[a-z0-9]$",
							"description": "The column name, lowercase with underscores.",
						},
						"type": map[string]any{
							"type":        "string",
							"enum":        types,
							"description": typeDoc,
						},
						"required": map[string]any{
							"type":        "boolean",
							"description": "NOT NULL in the schema, and validated as present in the request.",
						},
						"unique": map[string]any{
							"type":        "boolean",
							"description": "A unique index, scoped by tenant when the module is.",
						},
					},
				},
			},
			"permissions": map[string]any{
				"type":        "object",
				"description": "Which roles may take which action. The generated Policy denies everything not listed here -- an action with no entry is an action nobody can take.",
				"propertyNames": map[string]any{
					"enum": Actions,
				},
				"additionalProperties": map[string]any{
					"type":        "array",
					"minItems":    1,
					"items":       map[string]any{"type": "string"},
					"description": "The roles allowed to take this action.",
				},
			},
		},
		"examples": []any{
			map[string]any{
				"version":     Version,
				"name":        "invoice",
				"description": "An invoice issued to a customer.",
				"tenant":      true,
				"fields": []any{
					map[string]any{"name": "reference", "type": "string", "required": true, "unique": true},
					map[string]any{"name": "customer_email", "type": "email", "required": true},
					map[string]any{"name": "total", "type": "money", "required": true},
					map[string]any{"name": "due_date", "type": "date"},
					map[string]any{"name": "paid", "type": "bool"},
					map[string]any{"name": "notes", "type": "text"},
				},
				"permissions": map[string]any{
					"view":   []string{"admin", "member"},
					"list":   []string{"admin", "member"},
					"create": []string{"admin"},
					"update": []string{"admin"},
					"delete": []string{"admin"},
				},
			},
		},
	}

	body, err := json.MarshalIndent(schema, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("rendering the schema: %w", err)
	}
	return append(body, '\n'), nil
}

func describeTypes() string {
	names := make([]string, 0, len(Types))
	for name := range Types {
		names = append(names, name)
	}
	sort.Strings(names)

	out := ""
	for i, name := range names {
		if i > 0 {
			out += "; "
		}
		out += name + " is " + Types[name]
	}
	return out + "."
}
