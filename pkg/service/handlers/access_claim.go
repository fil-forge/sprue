package handlers

import (
	"context"
	"fmt"

	"github.com/fil-forge/go-libstoracha/capabilities/access"
	ucancap "github.com/fil-forge/go-libstoracha/capabilities/ucan"
	"github.com/fil-forge/go-ucanto/core/delegation"
	"github.com/fil-forge/go-ucanto/core/invocation"
	"github.com/fil-forge/go-ucanto/core/receipt/fx"
	"github.com/fil-forge/go-ucanto/core/result"
	"github.com/fil-forge/go-ucanto/core/result/failure"
	"github.com/fil-forge/go-ucanto/did"
	"github.com/fil-forge/go-ucanto/principal/absentee"
	"github.com/fil-forge/go-ucanto/server"
	"github.com/fil-forge/go-ucanto/ucan"
	"github.com/fil-forge/go-ucanto/validator"
	"github.com/fil-forge/sprue/pkg/identity"
	"github.com/fil-forge/sprue/pkg/internal/ipldutil"
	"github.com/fil-forge/sprue/pkg/lib/didmailto"
	"github.com/fil-forge/sprue/pkg/lib/errors"
	"github.com/fil-forge/sprue/pkg/lib/ucans"
	"github.com/fil-forge/sprue/pkg/store"
	delegation_store "github.com/fil-forge/sprue/pkg/store/delegation"
	"github.com/ipfs/go-cid"
	"go.uber.org/zap"
)

const InvalidClaimAudienceErrorName = "InvalidClaimAudience"

var ErrInvalidClaimAudience = errors.New(InvalidClaimAudienceErrorName, "invalid claim audience DID")

// WithAccessClaimMethod registers the access/claim handler.
func WithAccessClaimMethod(id *identity.Identity, delegationStore delegation_store.Store, logger *zap.Logger) server.Option {
	return server.WithServiceMethod(
		access.ClaimAbility,
		server.Provide(
			access.Claim,
			AccessClaimHandler(id, delegationStore, logger),
		),
	)
}

func AccessClaimHandler(id *identity.Identity, delegationStore delegation_store.Store, logger *zap.Logger) server.HandlerFunc[access.ClaimCaveats, access.ClaimOk, failure.IPLDBuilderFailure] {
	log := logger.With(zap.String("handler", access.ClaimAbility))
	return func(ctx context.Context,
		cap ucan.Capability[access.ClaimCaveats],
		inv invocation.Invocation,
		iCtx server.InvocationContext,
	) (result.Result[access.ClaimOk, failure.IPLDBuilderFailure], fx.Effects, error) {
		agent := inv.Issuer().DID()
		audience, err := did.Parse(cap.With())
		if err != nil {
			log.Warn("invalid audience", zap.String("audience", cap.With()))
			return result.Error[access.ClaimOk, failure.IPLDBuilderFailure](ErrInvalidClaimAudience), nil, nil
		}

		log := log.With(
			zap.Stringer("agent", agent),
			zap.Stringer("audience", audience),
		)
		log.Debug("claiming delegations")

		dlgs := map[cid.Cid]delegation.Delegation{}
		var cursor *string
		for {
			var opts []delegation_store.ListByAudienceOption
			if cursor != nil {
				opts = append(opts, delegation_store.WithListByAudienceCursor(*cursor))
			}
			page, err := delegationStore.ListByAudience(ctx, audience, opts...)
			if err != nil {
				return nil, nil, fmt.Errorf("listing delegations: %w", err)
			}
			for _, dlg := range page.Results {
				root, err := ipldutil.ToCID(dlg.Link())
				if err != nil {
					log.Warn("invalid delegation CID", zap.String("root", dlg.Link().String()), zap.Error(err))
					continue
				}
				dlgs[root] = dlg
			}
			if page.Cursor == nil {
				break
			}
			cursor = page.Cursor
		}

		refreshedDlgs := map[cid.Cid]delegation.Delegation{}
		// Find any attested ucan:* delegations and replace them with fresh ones.
		for root, dlg := range dlgs {
			if len(dlg.Capabilities()) == 0 {
				log.Warn("delegation with no capabilities", zap.String("root", dlg.Link().String()))
				refreshedDlgs[root] = dlg
				continue
			}

			// Ignore delegations that aren't attestations, and ours.
			cap := dlg.Capabilities()[0]

			var err error
			match, err := ucancap.Attest.Match(validator.NewSource(cap, dlg))
			if err != nil {
				refreshedDlgs[root] = dlg
				continue
			}
			if match.Value().With() != id.DID() {
				refreshedDlgs[root] = dlg
				continue
			}

			// Ignore invalid attestations.
			if valid, _ := ucan.VerifySignature(dlg.Data(), id.Signer.Verifier()); !valid {
				refreshedDlgs[root] = dlg
				continue
			}
			if ucan.IsTooEarly(dlg) || ucan.IsExpired(dlg) {
				refreshedDlgs[root] = dlg
				continue
			}

			attestedDlgRoot, err := ipldutil.ToCID(match.Value().Nb().Proof)
			if err != nil {
				log.Warn("invalid proof CID in attestation", zap.String("proof", match.Value().Nb().Proof.String()), zap.Error(err))
				refreshedDlgs[root] = dlg
				continue
			}

			// Ignore attestations of delegations we don't have.
			attestedDlg, ok := dlgs[attestedDlgRoot]
			if !ok {
				refreshedDlgs[root] = dlg
				continue
			}

			// Create new session proofs for the attested delegation.
			sessionPrfs, err := createSessionProofsForLogin(
				ctx,
				id.Signer,
				delegationStore,
				attestedDlg,
			)
			if err != nil {
				log.Error("failed to create session proofs for login", zap.Error(err))
				return nil, nil, fmt.Errorf("creating session proofs for login: %w", err)
			}
			for _, d := range sessionPrfs {
				root, err := ipldutil.ToCID(d.Link())
				if err != nil {
					return nil, nil, fmt.Errorf("getting CID of session proof delegation: %w", err)
				}
				refreshedDlgs[root] = d
			}
		}

		mdl := access.DelegationsModel{Values: map[string][]byte{}}
		for _, d := range dlgs {
			k := d.Link().String()
			v, err := ucans.ArchiveDelegations(d)
			if err != nil {
				log.Error("failed to archive delegation", zap.Error(err))
				return nil, nil, fmt.Errorf("archiving delegation: %w", err)
			}
			mdl.Keys = append(mdl.Keys, k)
			mdl.Values[k] = v
		}

		return result.Ok[access.ClaimOk, failure.IPLDBuilderFailure](access.ClaimOk{
			Delegations: mdl,
		}), nil, nil
	}
}

func createSessionProofsForLogin(
	ctx context.Context,
	service ucan.Signer,
	delegationStore delegation_store.Store,
	loginDlg delegation.Delegation,
) ([]delegation.Delegation, error) {
	// These should always be accounts (did:mailto:), but if one's not, skip it.
	account, err := didmailto.Parse(loginDlg.Issuer().DID().String())
	if err != nil {
		return []delegation.Delegation{}, nil
	}

	accountDlgs, err := store.Collect(ctx, func(ctx context.Context, options store.PaginationConfig) (store.Page[delegation.Delegation], error) {
		var opts []delegation_store.ListByAudienceOption
		if options.Cursor != nil {
			opts = append(opts, delegation_store.WithListByAudienceCursor(*options.Cursor))
		}
		return delegationStore.ListByAudience(ctx, account, opts...)
	})
	if err != nil {
		return nil, fmt.Errorf("collecting delegations for account: %w", err)
	}

	caps := make([]ucan.Capability[ucan.NoCaveats], 0, len(loginDlg.Capabilities()))
	for _, cap := range loginDlg.Capabilities() {
		caps = append(caps, ucan.NewCapability(cap.Can(), cap.With(), ucan.NoCaveats{}))
	}

	dlg, attestation, err := createSessionProofs(
		service,
		absentee.From(account),
		loginDlg.Audience(),
		toFactBuilders(loginDlg.Facts()),
		caps,
		// We include all the delegations to the account so that the agent will
		// have delegation chains to all the delegated resources.
		// We should actually filter out only delegations that support delegated
		// capabilities, but for now we just include all of them since we only
		// implement sudo access anyway.
		accountDlgs,
	)
	if err != nil {
		return nil, fmt.Errorf("creating session proofs: %w", err)
	}
	return []delegation.Delegation{dlg, attestation}, nil
}
