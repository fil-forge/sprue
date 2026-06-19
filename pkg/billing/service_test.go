package billing

import (
	"context"
	"testing"

	"github.com/fil-forge/libforge/attestation/didmailto"
	customermemory "github.com/fil-forge/sprue/pkg/store/customer/memory"
	"github.com/fil-forge/ucantone/did"
	"github.com/stretchr/testify/require"
)

func mustMailtoDID(t *testing.T, email string) did.DID {
	t.Helper()
	d, err := didmailto.New(email)
	require.NoError(t, err)
	return d
}

func mustDID(t *testing.T, s string) did.DID {
	t.Helper()
	d, err := did.Parse(s)
	require.NoError(t, err)
	return d
}

func TestPaymentPlan(t *testing.T) {
	ctx := context.Background()

	t.Run("returns product for existing customer", func(t *testing.T) {
		store := customermemory.New()
		account := mustMailtoDID(t, "alice@example.com")
		product := mustDID(t, "did:web:free.web3.storage")

		err := store.Add(ctx, account, nil, product, nil, nil)
		require.NoError(t, err)

		svc := NewService(store)
		plan, err := svc.PaymentPlan(ctx, account)
		require.NoError(t, err)
		require.Equal(t, product, plan)
	})

	t.Run("returns ErrMissingPaymentPlan for unknown account", func(t *testing.T) {
		store := customermemory.New()
		account := mustMailtoDID(t, "unknown@example.com")

		svc := NewService(store)
		_, err := svc.PaymentPlan(ctx, account)
		require.ErrorIs(t, err, ErrMissingPaymentPlan)
	})
}
