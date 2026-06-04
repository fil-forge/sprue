package handlers

import (
	"testing"

	"github.com/fil-forge/libforge/attestation"
	"github.com/fil-forge/libforge/attestation/didmailto"
	"github.com/fil-forge/libforge/commands/access"
	"github.com/fil-forge/sprue/internal/testutil"
	dlgmemory "github.com/fil-forge/sprue/pkg/store/delegation/memory"
	"github.com/fil-forge/ucantone/did"
	"github.com/fil-forge/ucantone/did/key"
	"github.com/fil-forge/ucantone/did/resolver"
	"github.com/fil-forge/ucantone/execution"
	"github.com/fil-forge/ucantone/ucan"
	"github.com/fil-forge/ucantone/ucan/command"
	"github.com/fil-forge/ucantone/ucan/invocation"
	"github.com/fil-forge/ucantone/validator"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

func TestAccessConfirmHandler(t *testing.T) {
	logger := zaptest.NewLogger(t)
	id := newTestIdentity(t)

	resolver := resolver.ByMethod{
		"key":    key.Resolver,
		"mailto": didmailto.NewResolver(id.DID()),
	}
	factories := validator.DefaultFactories()
	factories[attestation.Type] = attestation.NewVerifierFactory(resolver, factories)
	validationOpts := []validator.Option{
		validator.WithDIDResolver(resolver),
		validator.WithVerifierFactories(factories),
	}
	ctx := t.Context()

	t.Run("wrong subject", func(t *testing.T) {
		store := dlgmemory.New()
		handler := NewAccessConfirmHandler(id, store, logger)

		account := testutil.Must(didmailto.New("alice@example.com"))(t)
		agent := testutil.RandomIssuer(t)
		notService := testutil.RandomIssuer(t)

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
			id.Issuer,
			notService.DID(),
			&args,
			invocation.WithAudience(id.Issuer.DID()),
		)
		require.NoError(t, err)

		req := execution.NewRequest(ctx, inv)
		res, err := execution.NewResponse(req.Invocation().Task().Link(), execution.WithIssuer(id.Issuer))
		require.NoError(t, err)

		err = handler.Handler(req, res)
		require.NoError(t, err)

		_, err = access.Confirm.Unpack(res.Receipt())
		require.ErrorIs(t, err, access.ErrInvalidAccessConfirmSubject)
	})

	t.Run("invalid issuer DID", func(t *testing.T) {
		store := dlgmemory.New()
		handler := NewAccessConfirmHandler(id, store, logger)

		// A did:key (not a did:mailto) — didmailto.Parse will reject it.
		nonMailto := testutil.RandomIssuer(t)
		agent := testutil.RandomIssuer(t)

		args := access.ConfirmArguments{
			Cause:    testutil.RandomCID(t),
			Issuer:   nonMailto.DID(),
			Audience: agent.DID(),
			Attenuations: []access.CapabilityRequest{
				{Command: command.Top()},
			},
		}

		inv, err := access.Confirm.Invoke(
			id.Issuer,
			id.Issuer.DID(),
			&args,
			invocation.WithAudience(id.Issuer.DID()),
		)
		require.NoError(t, err)

		req := execution.NewRequest(ctx, inv)
		res, err := execution.NewResponse(req.Invocation().Task().Link(), execution.WithIssuer(id.Issuer))
		require.NoError(t, err)

		err = handler.Handler(req, res)
		require.NoError(t, err)

		_, err = access.Confirm.Unpack(res.Receipt())
		require.ErrorIs(t, err, access.ErrInvalidAccessConfirmIssuer)
	})

	t.Run("success", func(t *testing.T) {
		store := dlgmemory.New()
		handler := NewAccessConfirmHandler(id, store, logger)

		account := testutil.Must(didmailto.New("bob@example.com"))(t)
		agent := testutil.RandomIssuer(t)

		args := access.ConfirmArguments{
			Cause:    testutil.RandomCID(t),
			Issuer:   account,
			Audience: agent.DID(),
			Attenuations: []access.CapabilityRequest{
				{Command: command.Top()},
			},
		}

		inv, err := access.Confirm.Invoke(
			id.Issuer,
			id.Issuer.DID(),
			&args,
			invocation.WithAudience(id.Issuer.DID()),
		)
		require.NoError(t, err)

		req := execution.NewRequest(ctx, inv)
		res, err := execution.NewResponse(req.Invocation().Task().Link(), execution.WithIssuer(id.Issuer))
		require.NoError(t, err)

		err = handler.Handler(req, res)
		require.NoError(t, err)

		ok, err := access.Confirm.Unpack(res.Receipt())
		require.NoError(t, err)

		// One account delegation + one delegation per attenuation
		require.Len(t, ok.Delegations, 2)

		page, err := store.ListByAudience(ctx, agent.DID())
		require.NoError(t, err)
		require.Len(t, page.Results, 2)

		var accountDlg, powerlineDlg ucan.Delegation
		for _, tok := range page.Results {
			dlg, isDlg := tok.(ucan.Delegation)
			require.True(t, isDlg, "stored token should be a delegation")
			if dlg.Subject() == account {
				accountDlg = dlg
			} else {
				powerlineDlg = dlg
			}
		}
		require.NotNil(t, accountDlg, "should have an account delegation")
		require.NotNil(t, powerlineDlg, "should have a powerline delegation")

		require.Equal(t, account, accountDlg.Issuer())
		require.Equal(t, agent.DID(), accountDlg.Audience())
		require.Equal(t, account, accountDlg.Subject())
		require.Equal(t, command.Top(), accountDlg.Command())

		// The powerline delegation should be the one we expected
		require.Equal(t, account, powerlineDlg.Issuer())
		require.Equal(t, agent.DID(), powerlineDlg.Audience())
		require.Equal(t, did.Undef, powerlineDlg.Subject())
		require.Equal(t, command.Top(), powerlineDlg.Command())
		require.Len(t, powerlineDlg.Policy().Statements(), 0)

		// Both delegations should be valid
		err = validator.ValidateToken(ctx, accountDlg, validationOpts...)
		require.NoError(t, err)
		err = validator.ValidateToken(ctx, powerlineDlg, validationOpts...)
		require.NoError(t, err)
	})

	t.Run("multiple capabilities", func(t *testing.T) {
		store := dlgmemory.New()
		handler := NewAccessConfirmHandler(id, store, logger)

		account := testutil.Must(didmailto.New("carol@example.com"))(t)
		agent := testutil.RandomIssuer(t)

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
			id.Issuer,
			id.Issuer.DID(),
			&args,
			invocation.WithAudience(id.Issuer.DID()),
		)
		require.NoError(t, err)

		req := execution.NewRequest(ctx, inv)
		res, err := execution.NewResponse(req.Invocation().Task().Link(), execution.WithIssuer(id.Issuer))
		require.NoError(t, err)

		err = handler.Handler(req, res)
		require.NoError(t, err)

		ok, err := access.Confirm.Unpack(res.Receipt())
		require.NoError(t, err)

		// One account delegation + one delegation per attenuation
		require.Len(t, ok.Delegations, 3)

		page, err := store.ListByAudience(ctx, agent.DID())
		require.NoError(t, err)
		require.Len(t, page.Results, 3)

		// Separate the account delegation from the powerline delegations.
		var blobDlg, uploadDlg ucan.Delegation
		for _, tok := range page.Results {
			dlg, isDlg := tok.(ucan.Delegation)
			require.True(t, isDlg, "stored token should be a delegation")
			if dlg.Subject() == account {
				continue // account delegation; skip detailed checks here
			}
			if dlg.Command() == command.MustParse("/blob/add") {
				blobDlg = dlg
			} else if dlg.Command() == command.MustParse("/upload/add") {
				uploadDlg = dlg
			}
		}
		require.NotNil(t, blobDlg, "should have a /blob/add delegation")
		require.NotNil(t, uploadDlg, "should have an /upload/add delegation")

		require.Equal(t, account, blobDlg.Issuer())
		require.Equal(t, agent.DID(), blobDlg.Audience())
		require.Equal(t, did.Undef, blobDlg.Subject())
		require.Equal(t, command.MustParse("/blob/add"), blobDlg.Command())
		require.Len(t, blobDlg.Policy().Statements(), 0)
		err = validator.ValidateToken(ctx, blobDlg, validationOpts...)
		require.NoError(t, err)

		require.Equal(t, account, uploadDlg.Issuer())
		require.Equal(t, agent.DID(), uploadDlg.Audience())
		require.Equal(t, did.Undef, uploadDlg.Subject())
		require.Equal(t, command.MustParse("/upload/add"), uploadDlg.Command())
		require.Len(t, uploadDlg.Policy().Statements(), 0)
		err = validator.ValidateToken(ctx, uploadDlg, validationOpts...)
		require.NoError(t, err)
	})
}
