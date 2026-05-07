package handlers

import (
	"context"
	"testing"

	"github.com/fil-forge/go-libstoracha/capabilities/access"
	"github.com/fil-forge/go-ucanto/core/invocation"
	"github.com/fil-forge/go-ucanto/core/result"
	"github.com/fil-forge/go-ucanto/did"
	"github.com/fil-forge/go-ucanto/ucan"
	"github.com/fil-forge/sprue/internal/testutil"
	"github.com/fil-forge/sprue/pkg/identity"
	"github.com/fil-forge/sprue/pkg/lib/didmailto"
	dlgmemory "github.com/fil-forge/sprue/pkg/store/delegation/memory"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

func TestAccessConfirmHandler(t *testing.T) {
	logger := zaptest.NewLogger(t)

	t.Run("wrong resource DID", func(t *testing.T) {
		id := newTestIdentity(t)
		store := dlgmemory.New()
		handler := AccessConfirmHandler(id, store, logger)

		agent, err := identity.New("")
		require.NoError(t, err)

		account := mustMailtoDID(t, "alice@example.com")
		causeCID := testutil.RandomCID(t)

		cap := ucan.NewCapability(
			access.ConfirmAbility,
			"did:key:z6MkhaXgBZDvotDkL5257faiztiGiC2QtKLGpbnnEGta2doK",
			access.ConfirmCaveats{
				Iss:   account,
				Aud:   agent.Signer.DID(),
				Att:   []access.CapabilityRequest{{Can: "*"}},
				Cause: testutil.Must(invocation.Invoke(agent.Signer, id.Signer, ucan.NewCapability("test/thing", id.DID(), ucan.NoCaveats{})))(t).Link(),
			},
		)
		_ = causeCID

		inv, err := invocation.Invoke(id.Signer, id.Signer, cap)
		require.NoError(t, err)

		res, _, err := handler(context.Background(), cap, inv, nil)
		require.NoError(t, err)

		_, fail := result.Unwrap(res)
		require.NotNil(t, fail)
	})

	t.Run("invalid agent DID", func(t *testing.T) {
		id := newTestIdentity(t)
		store := dlgmemory.New()
		handler := AccessConfirmHandler(id, store, logger)

		account := mustMailtoDID(t, "alice@example.com")
		badAud, err := did.Parse("did:mailto:example.com:alice")
		require.NoError(t, err)

		agent, err := identity.New("")
		require.NoError(t, err)

		causeInv, err := invocation.Invoke(agent.Signer, id.Signer, ucan.NewCapability("test/thing", id.DID(), ucan.NoCaveats{}))
		require.NoError(t, err)

		cap := ucan.NewCapability(
			access.ConfirmAbility,
			id.DID(),
			access.ConfirmCaveats{
				Iss:   account,
				Aud:   badAud,
				Att:   []access.CapabilityRequest{{Can: "*"}},
				Cause: causeInv.Link(),
			},
		)

		inv, err := invocation.Invoke(id.Signer, id.Signer, cap)
		require.NoError(t, err)

		res, _, err := handler(context.Background(), cap, inv, nil)
		require.NoError(t, err)

		_, fail := result.Unwrap(res)
		require.NotNil(t, fail)
	})

	t.Run("success", func(t *testing.T) {
		id := newTestIdentity(t)
		store := dlgmemory.New()
		handler := AccessConfirmHandler(id, store, logger)

		agent, err := identity.New("")
		require.NoError(t, err)

		account := mustMailtoDID(t, "bob@example.com")

		causeInv, err := invocation.Invoke(agent.Signer, id.Signer, ucan.NewCapability("test/thing", id.DID(), ucan.NoCaveats{}))
		require.NoError(t, err)

		cap := ucan.NewCapability(
			access.ConfirmAbility,
			id.DID(),
			access.ConfirmCaveats{
				Iss:   account,
				Aud:   agent.Signer.DID(),
				Att:   []access.CapabilityRequest{{Can: "*"}},
				Cause: causeInv.Link(),
			},
		)

		inv, err := invocation.Invoke(id.Signer, id.Signer, cap)
		require.NoError(t, err)

		res, _, err := handler(context.Background(), cap, inv, nil)
		require.NoError(t, err)

		ok, fail := result.Unwrap(res)
		require.Nil(t, fail)
		// Should return 2 delegations: the account->agent delegation and the service attestation
		require.Len(t, ok.Delegations.Keys, 2)
		for _, k := range ok.Delegations.Keys {
			require.NotEmpty(t, ok.Delegations.Values[k])
		}

		// Verify delegations were stored
		page, err := store.ListByAudience(context.Background(), agent.Signer.DID())
		require.NoError(t, err)
		require.Len(t, page.Results, 2)
	})

	t.Run("multiple capabilities", func(t *testing.T) {
		id := newTestIdentity(t)
		store := dlgmemory.New()
		handler := AccessConfirmHandler(id, store, logger)

		agent, err := identity.New("")
		require.NoError(t, err)

		account := mustMailtoDID(t, "carol@example.com")

		causeInv, err := invocation.Invoke(agent.Signer, id.Signer, ucan.NewCapability("test/thing", id.DID(), ucan.NoCaveats{}))
		require.NoError(t, err)

		cap := ucan.NewCapability(
			access.ConfirmAbility,
			id.DID(),
			access.ConfirmCaveats{
				Iss: account,
				Aud: agent.Signer.DID(),
				Att: []access.CapabilityRequest{
					{Can: "space/blob/add"},
					{Can: "upload/add"},
				},
				Cause: causeInv.Link(),
			},
		)

		inv, err := invocation.Invoke(id.Signer, id.Signer, cap)
		require.NoError(t, err)

		res, _, err := handler(context.Background(), cap, inv, nil)
		require.NoError(t, err)

		ok, fail := result.Unwrap(res)
		require.Nil(t, fail)
		require.Len(t, ok.Delegations.Keys, 2)
	})
}

func mustMailtoDID(t *testing.T, email string) did.DID {
	t.Helper()
	d, err := didmailto.New(email)
	require.NoError(t, err)
	return d
}
