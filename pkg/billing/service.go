package billing

import (
	"context"
	"fmt"

	"github.com/fil-forge/go-ucanto/did"
	"github.com/fil-forge/sprue/pkg/lib/errors"
	"github.com/fil-forge/sprue/pkg/store/customer"
)

const MissingPaymentPlanErrorName = "MissingPaymentPlan"

// ErrMissingPaymentPlan is returned when an account does not have a payment plan.
var ErrMissingPaymentPlan = errors.New(MissingPaymentPlanErrorName, "account does not have a payment plan")

type Service struct {
	customerStore customer.Store
}

func NewService(customerStore customer.Store) *Service {
	return &Service{
		customerStore: customerStore,
	}
}

// PaymentPlan returns the payment plan for a given account. It may return
// [ErrMissingPaymentPlan].
func (s *Service) PaymentPlan(ctx context.Context, account did.DID) (did.DID, error) {
	r, err := s.customerStore.Get(ctx, account)
	if err != nil {
		if errors.Is(err, customer.ErrCustomerNotFound) {
			return did.Undef, ErrMissingPaymentPlan
		}
		return did.Undef, fmt.Errorf("getting customer: %w", err)
	}
	return r.Product, nil
}
