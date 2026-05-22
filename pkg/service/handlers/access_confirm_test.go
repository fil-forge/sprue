package handlers

import (
	"bytes"
	"testing"

	"github.com/fil-forge/libforge/commands/access"
	"github.com/fil-forge/libforge/didmailto"
	"github.com/fil-forge/sprue/internal/testutil"
	dlgmemory "github.com/fil-forge/sprue/pkg/store/delegation/memory"
	edm "github.com/fil-forge/ucantone/errors/datamodel"
	"github.com/fil-forge/ucantone/execution"
	"github.com/fil-forge/ucantone/ucan/command"
	"github.com/fil-forge/ucantone/ucan/invocation"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

func TestAccessConfirmHandler(t *testing.T) {
	logger := zaptest.NewLogger(t)

	t.Run("wrong subject", func(t *testing.T) {
		id := newTestIdentity(t)
		store := dlgmemory.New()
		handler := NewAccessConfirmHandler(id, store, logger)

		account := testutil.Must(didmailto.New("alice@example.com"))(t)
		agent := testutil.RandomSigner(t)
		notService := testutil.RandomSigner(t)

		args := access.ConfirmArguments{
			Cause:    testutil.RandomCID(t),
			Issuer:   account,
			Audience: agent.DID(),
			Attenuations: []access.CapabilityRequest{
				{Command: command.Top()},
			},
		}

		// Subject is not id.Signer — handler should reject.
		inv, err := access.Confirm.Invoke(
			id.Signer,
			notService.DID(),
			&args,
			invocation.WithAudience(id.Signer.DID()),
		)
		require.NoError(t, err)

		req := execution.NewRequest(t.Context(), inv)
		res, err := execution.NewResponse(req.Invocation().Task().Link(), execution.WithSigner(id.Signer))
		require.NoError(t, err)

		err = handler.Handler(req, res)
		require.NoError(t, err)

		o, x := res.Receipt().Out().Unpack()
		require.Nil(t, o)
		require.NotNil(t, x)

		var model edm.ErrorModel
		require.NoError(t, model.UnmarshalCBOR(bytes.NewReader(x)))
		require.Equal(t, access.InvalidAccessConfirmSubjectErrorName, model.Name())
	})

	t.Run("invalid issuer DID", func(t *testing.T) {
		id := newTestIdentity(t)
		store := dlgmemory.New()
		handler := NewAccessConfirmHandler(id, store, logger)

		// A did:key (not a did:mailto) — didmailto.Parse will reject it.
		nonMailto := testutil.RandomSigner(t)
		agent := testutil.RandomSigner(t)

		args := access.ConfirmArguments{
			Cause:    testutil.RandomCID(t),
			Issuer:   nonMailto.DID(),
			Audience: agent.DID(),
			Attenuations: []access.CapabilityRequest{
				{Command: command.Top()},
			},
		}

		inv, err := access.Confirm.Invoke(
			id.Signer,
			id.Signer.DID(),
			&args,
			invocation.WithAudience(id.Signer.DID()),
		)
		require.NoError(t, err)

		req := execution.NewRequest(t.Context(), inv)
		res, err := execution.NewResponse(req.Invocation().Task().Link(), execution.WithSigner(id.Signer))
		require.NoError(t, err)

		err = handler.Handler(req, res)
		require.NoError(t, err)

		o, x := res.Receipt().Out().Unpack()
		require.Nil(t, o)
		require.NotNil(t, x)

		var model edm.ErrorModel
		require.NoError(t, model.UnmarshalCBOR(bytes.NewReader(x)))
		require.Equal(t, access.InvalidAccessConfirmIssuerErrorName, model.Name())
	})

	t.Run("success", func(t *testing.T) {
		id := newTestIdentity(t)
		store := dlgmemory.New()
		handler := NewAccessConfirmHandler(id, store, logger)

		account := testutil.Must(didmailto.New("bob@example.com"))(t)
		agent := testutil.RandomSigner(t)

		args := access.ConfirmArguments{
			Cause:    testutil.RandomCID(t),
			Issuer:   account,
			Audience: agent.DID(),
			Attenuations: []access.CapabilityRequest{
				{Command: command.Top()},
			},
		}

		inv, err := access.Confirm.Invoke(
			id.Signer,
			id.Signer.DID(),
			&args,
			invocation.WithAudience(id.Signer.DID()),
		)
		require.NoError(t, err)

		req := execution.NewRequest(t.Context(), inv)
		res, err := execution.NewResponse(req.Invocation().Task().Link(), execution.WithSigner(id.Signer))
		require.NoError(t, err)

		err = handler.Handler(req, res)
		require.NoError(t, err)

		o, x := res.Receipt().Out().Unpack()
		require.Nil(t, x)
		require.NotNil(t, o)

		var ok access.ConfirmOK
		require.NoError(t, ok.UnmarshalCBOR(bytes.NewReader(o)))
		// One delegation link per attenuation.
		require.Len(t, ok.Delegations, 1)

		// Store holds the delegation and its attestation, both keyed by the agent.
		page, err := store.ListByAudience(t.Context(), agent.DID())
		require.NoError(t, err)
		require.Len(t, page.Results, 2)
	})

	t.Run("multiple capabilities", func(t *testing.T) {
		id := newTestIdentity(t)
		store := dlgmemory.New()
		handler := NewAccessConfirmHandler(id, store, logger)

		account := testutil.Must(didmailto.New("carol@example.com"))(t)
		agent := testutil.RandomSigner(t)

		args := access.ConfirmArguments{
			Cause:    testutil.RandomCID(t),
			Issuer:   account,
			Audience: agent.DID(),
			Attenuations: []access.CapabilityRequest{
				{Command: command.MustParse("/blob/add")},
				{Command: command.MustParse("/upload/add")},
			},
		}

		inv, err := access.Confirm.Invoke(
			id.Signer,
			id.Signer.DID(),
			&args,
			invocation.WithAudience(id.Signer.DID()),
		)
		require.NoError(t, err)

		req := execution.NewRequest(t.Context(), inv)
		res, err := execution.NewResponse(req.Invocation().Task().Link(), execution.WithSigner(id.Signer))
		require.NoError(t, err)

		err = handler.Handler(req, res)
		require.NoError(t, err)

		o, x := res.Receipt().Out().Unpack()
		require.Nil(t, x)
		require.NotNil(t, o)

		var ok access.ConfirmOK
		require.NoError(t, ok.UnmarshalCBOR(bytes.NewReader(o)))
		require.Len(t, ok.Delegations, 2)

		// Two attenuations → two delegations and two attestations stored.
		page, err := store.ListByAudience(t.Context(), agent.DID())
		require.NoError(t, err)
		require.Len(t, page.Results, 4)
	})
}
