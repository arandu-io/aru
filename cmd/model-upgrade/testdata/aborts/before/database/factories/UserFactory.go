package factories

import (
	"github.com/arandu-io/hesape/database/model"
	factory "github.com/arandu-io/hesape/database/model/factories"
	"github.com/arandu-io/hesape/faker"

	models "example.test/aborts/app/Models"
)

// UserFactory returns the factory of users.
func UserFactory(db model.DB) *factory.Factory[models.User] {
	return factory.For(models.Users(db), func(f faker.Faker) models.User {
		return models.User{}
	})
}

// withParent links a factory to another through the generic helper.
func withParent(db model.DB) {
	factory.ForParent(UserFactory(db), UserFactory(db), func(c, p *models.User) {})
}
