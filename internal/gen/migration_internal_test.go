package gen

import "testing"

// A column added to a table that already has rows must never be NULL, because
// the scan this same generator emits reads every column straight into its Go
// type -- there is no sql.NullInt64 anywhere in it.
//
// Without this: `ALTER TABLE posts ADD COLUMN views INTEGER`, nullable, no
// default, no backfill, while PostRepository.scan reads it into a plain int.
// During the rollout, every replica still on the old binary answers "converting
// NULL to int is unsupported" on every read of that table -- and so does the new
// one, until something writes each row.
func TestAnAddedColumnIsNeverNull(t *testing.T) {
	for typ := range types {
		if (Field{Name: "probe", Type: typ}).SQLZero() == "" {
			t.Errorf("a %s column added to a live table would be NULL, and the scan this generator writes cannot read it", typ)
		}
	}
}
