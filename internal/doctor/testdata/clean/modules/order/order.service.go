package order

import (
	"context"

	"github.com/arandu-io/framework/data"
	"github.com/arandu-io/framework/observability"
	"github.com/arandu-io/framework/security"
)

// Service holds the business rules. It receives its dependencies through the
// constructor -- explicit wiring, no container.
type Service struct {
	repo   *Repo
	policy Policy
}

// NewService wires the module.
func NewService(repo *Repo) *Service { return &Service{repo: repo} }

// Create walks the mandatory path: validate, Authorize, Grant, Repository.
// There is no other order that compiles.
func (s *Service) Create(ctx context.Context, actor security.Subject, in CreateRequest) (Order, error) {
	if errs := in.Validate(); errs.Any() {
		return Order{}, errs
	}

	candidate := Order{
		TenantID:  actor.Tenant,
		Reference: in.Reference,
		Total:     in.Total,
	}

	g, err := security.Authorize(ctx, s.policy, actor, ActionCreate, candidate)
	if err != nil {
		return Order{}, err
	}

	created, err := s.repo.Create(ctx, g, candidate)
	if err != nil {
		return Order{}, err
	}
	observability.FromContext(ctx).RecordEvent("order.created", created)
	return created, nil
}

// Get returns one order.
func (s *Service) Get(ctx context.Context, actor security.Subject, id string) (Order, error) {
	g, err := security.Authorize(ctx, s.policy, actor, ActionView, Order{})
	if err != nil {
		return Order{}, err
	}
	return s.repo.Find(ctx, g, id)
}

// List returns a page of orders.
func (s *Service) List(ctx context.Context, actor security.Subject, q data.Query) ([]Order, error) {
	g, err := security.Authorize(ctx, s.policy, actor, ActionView, Order{})
	if err != nil {
		return nil, err
	}
	return s.repo.List(ctx, g, q)
}

// Update changes the mutable fields.
//
// It reads before writing, so the policy decides against the stored row rather
// than against what the client claims the row is. Skipping this is how a check
// passes on attacker-supplied data.
func (s *Service) Update(ctx context.Context, actor security.Subject, in UpdateRequest) (Order, error) {
	if errs := in.Validate(); errs.Any() {
		return Order{}, errs
	}

	view, err := security.Authorize(ctx, s.policy, actor, ActionView, Order{})
	if err != nil {
		return Order{}, err
	}
	stored, err := s.repo.Find(ctx, view, in.ID)
	if err != nil {
		return Order{}, err
	}

	g, err := security.Authorize(ctx, s.policy, actor, ActionUpdate, stored)
	if err != nil {
		return Order{}, err
	}
	stored.Reference = in.Reference
	stored.Total = in.Total
	return s.repo.Update(ctx, g, stored)
}

// Delete removes a order.
func (s *Service) Delete(ctx context.Context, actor security.Subject, id string) error {
	view, err := security.Authorize(ctx, s.policy, actor, ActionView, Order{})
	if err != nil {
		return err
	}
	stored, err := s.repo.Find(ctx, view, id)
	if err != nil {
		return err
	}

	g, err := security.Authorize(ctx, s.policy, actor, ActionDelete, stored)
	if err != nil {
		return err
	}
	if err := s.repo.Delete(ctx, g, id); err != nil {
		return err
	}
	observability.FromContext(ctx).RecordEvent("order.deleted", stored)
	return nil
}

// arandu:begin custom
// Business rules beyond CRUD go here, and survive regeneration.
// arandu:end custom
