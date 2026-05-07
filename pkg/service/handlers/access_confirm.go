package handlers

import (
	"context"
	"fmt"

	"go.uber.org/zap"

	"github.com/fil-forge/go-libstoracha/capabilities/access"
	ucan_caps "github.com/fil-forge/go-libstoracha/capabilities/ucan"
	"github.com/fil-forge/go-ucanto/core/delegation"
	"github.com/fil-forge/go-ucanto/core/invocation"
	"github.com/fil-forge/go-ucanto/core/ipld"
	"github.com/fil-forge/go-ucanto/core/receipt/fx"
	"github.com/fil-forge/go-ucanto/core/result"
	"github.com/fil-forge/go-ucanto/core/result/failure"
	"github.com/fil-forge/go-ucanto/principal/absentee"
	"github.com/fil-forge/go-ucanto/principal/ed25519/verifier"
	"github.com/fil-forge/go-ucanto/server"
	"github.com/fil-forge/go-ucanto/ucan"
	"github.com/fil-forge/sprue/pkg/identity"
	"github.com/fil-forge/sprue/pkg/lib/errors"
	"github.com/fil-forge/sprue/pkg/lib/ucans"
	delegation_store "github.com/fil-forge/sprue/pkg/store/delegation"
	"github.com/ipfs/go-cid"
	"github.com/ipld/go-ipld-prime/node/basicnode"
)

const InvalidAccessConfirmDelegationErrorName = "InvalidAccessConfirmDelegation"

// WithAccessConfirmMethod registers the access/confirm handler.
func WithAccessConfirmMethod(id *identity.Identity, delegationStore delegation_store.Store, logger *zap.Logger) server.Option {
	return server.WithServiceMethod(
		access.ConfirmAbility,
		server.Provide(
			access.Confirm,
			AccessConfirmHandler(id, delegationStore, logger),
		),
	)
}

func AccessConfirmHandler(id *identity.Identity, delegationStore delegation_store.Store, logger *zap.Logger) server.HandlerFunc[access.ConfirmCaveats, access.ConfirmOk, failure.IPLDBuilderFailure] {
	log := logger.With(zap.String("handler", access.ConfirmAbility))
	return func(ctx context.Context,
		cap ucan.Capability[access.ConfirmCaveats],
		inv invocation.Invocation,
		iCtx server.InvocationContext,
	) (result.Result[access.ConfirmOk, failure.IPLDBuilderFailure], fx.Effects, error) {
		if cap.With() != id.DID() {
			log.Warn("not a valid delegation", zap.String("resource", cap.With()))
			return result.Error[access.ConfirmOk, failure.IPLDBuilderFailure](
				errors.New(InvalidAccessConfirmDelegationErrorName, "not a valid access/confirm delegation"),
			), nil, nil
		}

		// Create a absentee signer for the account that authorized the delegation
		account := absentee.From(cap.Nb().Iss)
		agent, err := verifier.Parse(cap.Nb().Aud.String())
		if err != nil {
			log.Warn("invalid audience", zap.Stringer("audience", cap.Nb().Aud))
			return result.Error[access.ConfirmOk, failure.IPLDBuilderFailure](
				errors.New(InvalidAccessConfirmDelegationErrorName, "invalid agent DID in delegation"),
			), nil, nil
		}

		abilities := []ucan.Ability{}
		for _, att := range cap.Nb().Att {
			abilities = append(abilities, att.Can)
		}

		log := log.With(
			zap.Stringer("agent", agent.DID()),
			zap.Stringer("account", account.DID()),
		)
		log.Debug("confirming access", zap.Strings("abilities", abilities))

		// In the future we should instead render a page and allow a user to select
		// which delegations they wish to re-delegate. Right now we just re-delegate
		// everything that was requested for all of the resources.
		var capabilities []ucan.Capability[ucan.NoCaveats]
		for _, att := range cap.Nb().Att {
			capabilities = append(capabilities, ucan.NewCapability(att.Can, "ucan:*", ucan.NoCaveats{}))
		}

		// Create session proofs, but containing no Space proofs. We'll store these,
		// and generate the Space proofs on access/claim.
		dlg, attestation, err := createSessionProofs(
			id.Signer,
			account,
			agent,
			[]ucan.FactBuilder{
				accessConfirmFact{
					accessRequest: cap.Nb().Cause,
					accessConfirm: inv.Link(),
				},
			},
			capabilities,
			[]delegation.Delegation{},
		)
		if err != nil {
			return nil, nil, fmt.Errorf("creating session proofs: %w", err)
		}

		dlgs := []delegation.Delegation{dlg, attestation}

		// Store the delegations so that they can be pulled during access/claim.
		// Since there is no invocation that contains these delegations, don't pass
		// a `cause` parameter.
		// TODO: we should invoke access/delegate here rather than interacting with
		// the delegations storage system directly.
		err = delegationStore.PutMany(ctx, dlgs, cid.Undef)
		if err != nil {
			log.Error("failed to store delegations", zap.Error(err))
			return nil, nil, fmt.Errorf("storing delegations: %w", err)
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

		return result.Ok[access.ConfirmOk, failure.IPLDBuilderFailure](access.ConfirmOk{
			Delegations: mdl,
		}), nil, nil
	}
}

type accessConfirmFact struct {
	accessRequest ipld.Link
	accessConfirm ipld.Link
}

func (f accessConfirmFact) ToIPLD() (map[string]ipld.Node, error) {
	return map[string]ipld.Node{
		"access/request": basicnode.NewLink(f.accessRequest),
		"access/confirm": basicnode.NewLink(f.accessConfirm),
	}, nil
}

// createSessionProofs creates a delegation from the account to the agent, and
// an attestation from the service to the agent referencing that delegation.
func createSessionProofs[C ucan.CaveatBuilder](
	service ucan.Signer,
	account ucan.Signer,
	agent ucan.Principal,
	facts []ucan.FactBuilder,
	capabilities []ucan.Capability[C],
	delegationProofs []delegation.Delegation,
) (delegation.Delegation, delegation.Delegation, error) {
	var proofs []delegation.Proof
	for _, d := range delegationProofs {
		proofs = append(proofs, delegation.FromDelegation(d))
	}

	// create an delegation on behalf of the account with an absent signature.
	dlg, err := delegation.Delegate(
		account,
		agent,
		capabilities,
		delegation.WithProof(proofs...),
		delegation.WithFacts(facts),
		// default to Infinity is reasonable here because
		// account consented to this.
		delegation.WithNoExpiration(),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("creating delegation: %w", err)
	}

	attestation, err := ucan_caps.Attest.Delegate(
		service,
		agent,
		service.DID().String(),
		ucan_caps.AttestCaveats{
			Proof: dlg.Link(),
		},
		delegation.WithFacts(facts),
		delegation.WithNoExpiration(),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("creating attestation: %w", err)
	}

	return dlg, attestation, nil
}
