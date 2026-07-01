package handlers

import (
	"context"
	"testing"

	customercmds "github.com/fil-forge/libforge/commands/customer"
	"github.com/fil-forge/libforge/identity"
	"github.com/fil-forge/sprue/internal/testutil"
	customerstore "github.com/fil-forge/sprue/pkg/store/customer"
	customer_store "github.com/fil-forge/sprue/pkg/store/customer/memory"
	"github.com/fil-forge/ucantone/did"
	"github.com/fil-forge/ucantone/execution"
	"github.com/fil-forge/ucantone/ucan/invocation"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

// invokeCustomerAdd builds a /customer/add invocation with the given subject,
// plus a signed response, ready to pass to the handler.
func invokeCustomerAdd(
	t *testing.T,
	ctx context.Context,
	id identity.Identity,
	subject did.DID,
	args *customercmds.AddArguments,
) (execution.Request, *execution.ExecResponse) {
	t.Helper()
	inv, err := customercmds.Add.Invoke(
		id.Issuer,
		subject,
		args,
		invocation.WithAudience(id.Issuer.DID()),
	)
	require.NoError(t, err)
	req := execution.NewRequest(ctx, inv)
	res, err := execution.NewResponse(req.Invocation().Task().Link(), execution.WithIssuer(id.Issuer))
	require.NoError(t, err)
	return req, res
}

func TestCustomerAddHandler(t *testing.T) {
	logger := zaptest.NewLogger(t)
	ctx := t.Context()
	id := newTestIdentity(t)

	t.Run("success", func(t *testing.T) {
		store := customer_store.New()
		handler := NewCustomerAddHandler(id, store, logger)

		customerDID := testutil.RandomDID(t)
		product := testutil.Must(did.Parse("did:web:free.web3.storage"))(t)
		account := "stripe:cus_9s6XKzkNRiz8i3"
		details := map[string]string{"plan": "pro"}

		// Subject is the service DID, as set by the upload client.
		req, res := invokeCustomerAdd(t, ctx, id, id.DID(), &customercmds.AddArguments{
			Customer:        customerDID,
			ExternalAccount: &account,
			Product:         product,
			Details:         details,
		})

		err := handler.Handler(req, res)
		require.NoError(t, err)

		_, err = customercmds.Add.Unpack(res.Receipt())
		require.NoError(t, err)

		rec, err := store.Get(ctx, customerDID)
		require.NoError(t, err)
		require.Equal(t, customerDID, rec.Customer)
		require.Equal(t, product, rec.Product)
		require.NotNil(t, rec.ExternalAccount)
		require.Equal(t, account, *rec.ExternalAccount)
		require.Equal(t, map[string]any{"plan": "pro"}, rec.Details)
	})

	t.Run("wrong subject", func(t *testing.T) {
		store := customer_store.New()
		handler := NewCustomerAddHandler(id, store, logger)

		customerDID := testutil.RandomDID(t)
		product := testutil.Must(did.Parse("did:web:free.web3.storage"))(t)
		notService := testutil.RandomIssuer(t)

		req, res := invokeCustomerAdd(t, ctx, id, notService.DID(), &customercmds.AddArguments{
			Customer: customerDID,
			Product:  product,
		})

		err := handler.Handler(req, res)
		require.NoError(t, err)

		_, err = customercmds.Add.Unpack(res.Receipt())
		require.ErrorIs(t, err, errInvalidCustomerSubject)

		// The customer must not have been written.
		_, err = store.Get(ctx, customerDID)
		require.ErrorIs(t, err, customerstore.ErrCustomerNotFound)
	})

	t.Run("duplicate customer", func(t *testing.T) {
		store := customer_store.New()
		handler := NewCustomerAddHandler(id, store, logger)

		customerDID := testutil.RandomDID(t)
		product := testutil.Must(did.Parse("did:web:free.web3.storage"))(t)

		require.NoError(t, store.Add(ctx, customerDID, nil, product, nil, nil))

		req, res := invokeCustomerAdd(t, ctx, id, id.DID(), &customercmds.AddArguments{
			Customer: customerDID,
			Product:  product,
		})

		err := handler.Handler(req, res)
		require.NoError(t, err)

		_, err = customercmds.Add.Unpack(res.Receipt())
		require.ErrorIs(t, err, customerstore.ErrCustomerExists)
	})
}
