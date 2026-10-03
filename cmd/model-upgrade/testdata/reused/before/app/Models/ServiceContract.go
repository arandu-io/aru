package models

import (
	"context"

	"github.com/arandu-io/hesape/auth"
	"github.com/arandu-io/hesape/database/model"
)

// ServiceContract binds a publisher to a professional.
type ServiceContract struct {
	model.Model[ServiceContract]

	ID             string `db:"id"`
	TenantID       string `db:"tenant_id"`
	PublisherID    string `db:"publisher_id"`
	ProfessionalID string `db:"professional_id"`
}

// ServiceContracts returns the model for service_contracts.
func ServiceContracts(db model.DB) *model.Model[ServiceContract] {
	return model.NewModel[ServiceContract]("service_contracts", db, nil, nil).UseUniqueIDs()
}

// PartyContracts counts both sides of a party on one held model, inside the
// package that declares the constructor.
func PartyContracts(ctx context.Context, db model.DB, g auth.Grant, party string) (int64, error) {
	rows := ServiceContracts(db)
	published, err := rows.Where("publisher_id", party).Count(ctx, g)
	if err != nil {
		return 0, err
	}
	hired, err := rows.Where("professional_id", party).Count(ctx, g)
	return published + hired, err
}
