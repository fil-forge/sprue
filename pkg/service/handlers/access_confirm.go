package handlers

import (
	"fmt"

	"github.com/fil-forge/libforge/capabilities/access"
	"github.com/fil-forge/libforge/capabilities/ucan/attest"
	"github.com/fil-forge/libforge/didmailto"
	"github.com/fil-forge/sprue/pkg/identity"
	delegation_store "github.com/fil-forge/sprue/pkg/store/delegation"
	"github.com/fil-forge/ucantone/errors"
	"github.com/fil-forge/ucantone/execution/bindexec"
	"github.com/fil-forge/ucantone/ipld"
	"github.com/fil-forge/ucantone/ipld/datamodel"
	"github.com/fil-forge/ucantone/principal/absentee"
	"github.com/fil-forge/ucantone/ucan"
	"github.com/fil-forge/ucantone/ucan/container"
	"github.com/fil-forge/ucantone/ucan/delegation"
	"github.com/fil-forge/ucantone/ucan/invocation"
	"github.com/ipfs/go-cid"
	"go.uber.org/zap"
)

const InvalidAccessConfirmInvocationErrorName = "InvalidAccessConfirmInvocation"

func NewAccessConfirmHandler(id *identity.Identity, delegationStore delegation_store.Store, logger *zap.Logger) Handler {
	log := logger.With(zap.String("handler", access.ConfirmCommand))
	return Handler{
		Capability: access.Confirm,
		Handler: bindexec.NewHandler(func(
			req *bindexec.Request[*access.ConfirmArguments],
			res *bindexec.Response[*access.ConfirmOK],
		) error {
			args := req.Task().BindArguments()
			if req.Invocation().Subject().DID() != id.Signer.DID() {
				log.Warn("not a valid invocation", zap.Stringer("subject", req.Invocation().Subject().DID()))
				return res.SetFailure(errors.New(InvalidAccessConfirmInvocationErrorName, "not a valid access/confirm delegation"))
			}

			accountDID, err := didmailto.Parse(args.Issuer.DID().String())
			if err != nil {
				log.Warn("invalid issuer DID", zap.Stringer("issuer", args.Issuer.DID()), zap.Error(err))
				return res.SetFailure(errors.New(InvalidAccessConfirmInvocationErrorName, "invalid issuer DID in delegation"))
			}

			// Create a absentee signer for the account that authorized the delegation
			account := absentee.From(accountDID)
			agent := args.Audience

			cmds := make([]string, 0, len(args.Attenuations))
			for _, att := range args.Attenuations {
				cmds = append(cmds, att.Command.String())
			}

			log := log.With(
				zap.Stringer("agent", agent.DID()),
				zap.Stringer("account", account.DID()),
				zap.Stringer("cause", args.Cause),
				zap.Strings("commands", cmds),
			)
			log.Debug("confirming access")

			// Create session proofs, but containing no Space proofs. We'll store these,
			// and generate the Space proofs on access/claim.
			delegations, attestations, err := createSessionProofs(
				id.Signer,
				account,
				agent,
				args.Attenuations,
				datamodel.Map{
					access.RequestMetaKey: args.Cause,
					access.ConfirmMetaKey: req.Invocation().Task().Link(),
				},
			)
			if err != nil {
				return fmt.Errorf("creating session proofs: %w", err)
			}

			links := make([]cid.Cid, 0, len(delegations))
			tokens := make([]ucan.Token, 0, len(delegations)+len(attestations))
			for _, d := range delegations {
				tokens = append(tokens, d)
				links = append(links, d.Link())
			}
			for _, a := range attestations {
				tokens = append(tokens, a)
			}

			// Store the delegations so that they can be pulled during /access/claim.
			// Since there is no invocation that contains these delegations, don't pass
			// a `cause` parameter.
			// TODO: we should invoke /access/delegate here rather than interacting
			// with the delegations storage system directly.
			err = delegationStore.PutMany(req.Context(), tokens, cid.Undef)
			if err != nil {
				log.Error("failed to store delegations", zap.Error(err))
				return fmt.Errorf("storing delegations: %w", err)
			}

			// Include the delegations and attestations in the response metadata.
			res.SetMetadata(container.New(
				container.WithDelegations(delegations...),
				container.WithInvocations(attestations...),
			))

			return res.SetSuccess(&access.ConfirmOK{Delegations: links})
		}),
	}
}

// createSessionProofs creates delegations from the account to the agent, and
// attestations from the service to the agent referencing those delegations.
func createSessionProofs(
	service ucan.Signer,
	account absentee.Signer,
	agent ucan.Principal,
	attenuations []access.CapabilityRequest,
	meta ipld.Map,
) ([]ucan.Delegation, []ucan.Invocation, error) {
	delegations := make([]ucan.Delegation, 0, len(attenuations))
	attestations := make([]ucan.Invocation, 0, len(attenuations))

	for _, req := range attenuations {
		dlg, err := delegation.Delegate(
			account,
			agent,
			// TODO: optionally set subject in capability request
			// no subject (powerline) will apply to all spaces present and future
			nil,
			req.Command,
			delegation.WithMetadata(meta),
			// default to Infinity is reasonable here because
			// account consented to this.
			delegation.WithNoExpiration(),
		)
		if err != nil {
			return nil, nil, fmt.Errorf("creating delegation: %w", err)
		}
		delegations = append(delegations, dlg)

		attestation, err := attest.Proof.Invoke(
			service,
			service,
			&attest.ProofArguments{
				Proof: dlg.Link(),
			},
			invocation.WithAudience(agent),
			invocation.WithMetadata(meta),
			invocation.WithNoExpiration(),
		)
		if err != nil {
			return nil, nil, fmt.Errorf("creating attestation: %w", err)
		}
		attestations = append(attestations, attestation)
	}

	return delegations, attestations, nil
}
