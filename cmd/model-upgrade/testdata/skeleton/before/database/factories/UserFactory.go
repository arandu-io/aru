// Package factories builds rows with plausible values, for seeders and tests.
package factories

import (
	"strings"
	"time"

	"github.com/arandu-io/framework/data"
	factory "github.com/arandu-io/hesape/database/model/factories"
	"github.com/arandu-io/hesape/faker"

	models "example.test/skeleton/app/Models"
)

// UnusablePassword is the password column of an account made by the factory.
const UnusablePassword = "!"

// verifiedFrom and verifiedUntil bound the verification time a made account
// carries.
var (
	verifiedFrom  = time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	verifiedUntil = time.Date(2026, time.June, 30, 0, 0, 0, 0, time.UTC)
)

// UserFactory returns the factory of users over db.
//
//	authors, err := factories.UserFactory(db).Count(3).Create(ctx, g)
func UserFactory(db *data.DB) *factory.Factory[models.User] {
	return factory.For(models.Users(db), func(f faker.Faker) models.User {
		// The faker's handles repeat across rows, and an address is unique in
		// its tenant: a fragment of a drawn id keeps a batch apart.
		handle := f.UserName() + "." + strings.SplitN(f.UUID(), "-", 2)[0]
		verified := f.Time(verifiedFrom, verifiedUntil)
		return models.User{
			Name:       f.Name(),
			Email:      handle + "@example.test",
			Password:   UnusablePassword,
			Roles:      models.Roles{models.RoleMember},
			VerifiedAt: &verified,
		}
	})
}

// arandu:begin custom
// Named states go here, and survive regeneration: a factory with one thing
// said about it, built on the one above.
// arandu:end custom
