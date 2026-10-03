package services

import (
	"context"

	"github.com/arandu-io/hesape/auth"
	"github.com/arandu-io/hesape/database/model"

	models "example.test/reused/app/Models"
)

// ContractService reads the contracts of a party.
type ContractService struct{ db model.DB }

// Find starts two chains on one held model, the second meant to start from
// nothing: correct while the constructor opened a query per chain.
func (s *ContractService) Find(ctx context.Context, g auth.Grant, party string) (*models.ServiceContract, error) {
	rows := models.ServiceContracts(s.db)
	found, err := rows.Where("publisher_id", party).First(ctx, g)
	if err != nil || found != nil {
		return found, err
	}
	found, err = rows.Where("professional_id", party).First(ctx, g)
	return found, err
}

// Published holds the model and starts one chain on it, which is the same
// query before and after.
func (s *ContractService) Published(ctx context.Context, g auth.Grant, party string) (*models.ServiceContract, error) {
	rows := models.ServiceContracts(s.db)
	return rows.Where("publisher_id", party).First(ctx, g)
}

// Handed gives the held model to a helper, which may start any number of
// chains on it.
func (s *ContractService) Handed(ctx context.Context, g auth.Grant, party string) error {
	var rows = models.ServiceContracts(s.db)
	return audit(ctx, g, rows, party)
}

// Each starts a chain on the one held model in every iteration.
func (s *ContractService) Each(ctx context.Context, g auth.Grant, parties []string) error {
	rows := models.ServiceContracts(s.db)
	for _, party := range parties {
		if _, err := rows.Where("publisher_id", party).Delete(ctx, g); err != nil {
			return err
		}
	}
	return nil
}

// Fresh holds a new model in every iteration and starts one chain on it.
func (s *ContractService) Fresh(ctx context.Context, g auth.Grant, parties []string) error {
	for _, party := range parties {
		rows := models.ServiceContracts(s.db)
		if _, err := rows.Where("publisher_id", party).Delete(ctx, g); err != nil {
			return err
		}
	}
	return nil
}

// Later captures the held model in a function its caller may run again.
func (s *ContractService) Later(party string) func(context.Context, auth.Grant) (int64, error) {
	rows := models.ServiceContracts(s.db)
	return func(ctx context.Context, g auth.Grant) (int64, error) {
		return rows.Where("publisher_id", party).Count(ctx, g)
	}
}

// Shadowed starts one chain on the held model: the rows of the loop are
// another variable of the same name.
func (s *ContractService) Shadowed(ctx context.Context, g auth.Grant, batches [][]string) (int64, error) {
	rows := models.ServiceContracts(s.db)
	total, err := rows.Where("publisher_id", "p-1").Count(ctx, g)
	for _, rows := range batches {
		total += int64(len(rows) + cap(rows))
	}
	return total, err
}

// Assigned binds the model with a plain assignment, then starts two chains.
func (s *ContractService) Assigned(ctx context.Context, g auth.Grant, party string) (int64, error) {
	var rows counter
	rows = models.ServiceContracts(s.db)
	if n, err := rows.Where("publisher_id", party).Count(ctx, g); err != nil || n > 0 {
		return n, err
	}
	return rows.Where("professional_id", party).Count(ctx, g)
}

// everyone is one model every caller of the package shares.
var everyone = models.ServiceContracts(nil)

// counter is what Assigned asks of the model.
type counter interface {
	Where(column any, args ...any) interface {
		Count(context.Context, auth.Grant) (int64, error)
	}
}

func audit(ctx context.Context, g auth.Grant, rows any, party string) error { return nil }
