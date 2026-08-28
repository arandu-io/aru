package services

import (
	"context"

	"github.com/arandu-io/framework/security"

	models "example.test/p/app/Models"
	repositories "example.test/p/app/Repositories"
)

// BillingService reads charges and returns them without re-authorizing the row.
//
// The first Authorize answers whether the caller may look at all. The second,
// with the row that was read, is the object-level decision.
type BillingService struct {
	charges *repositories.BillingRepository
	policy  security.Policy[models.Charge]
}

// ViewCharge is a violation: it reads a row and does not re-authorize it.
//
// The Authorize call on the zero value grants permission to look at the table.
// The Find call reads the row. Nothing checks whether this caller may read
// this specific row -- any user of the same tenant will see it.
func (s *BillingService) ViewCharge(ctx context.Context, actor security.Subject, id string) (models.Charge, error) {
	g, err := security.Authorize(ctx, s.policy, actor, "billing.view", models.Charge{})
	if err != nil {
		return models.Charge{}, err
	}

	charge, err := s.charges.Find(ctx, g, id)
	return charge, err
}

// ShowCharge is the same violation spelled through a Get.
//
// The spelling is the whole reason this method exists. A model builder's listing
// terminal is also called Get, and a listing after an Authorize is not a hole --
// the Grant that was handed in is what scoped the statement, and there is no
// second object to ask about. What makes this one a hole is the id: the call is
// pointed at one row, and nothing asks the policy about that row.
func (s *BillingService) ShowCharge(ctx context.Context, actor security.Subject, id string) (models.Charge, error) {
	g, err := security.Authorize(ctx, s.policy, actor, "billing.view", models.Charge{})
	if err != nil {
		return models.Charge{}, err
	}

	charge, err := s.charges.Get(ctx, g, id)
	return charge, err
}
