package handlers

import (
	"fmt"

	"github.com/ipfs/go-cid"
	"go.uber.org/zap"

	"github.com/fil-forge/libforge/attestation"
	"github.com/fil-forge/libforge/attestation/didmailto"
	"github.com/fil-forge/libforge/commands/access"
	"github.com/fil-forge/libforge/identity"
	"github.com/fil-forge/ucantone/binding"
	"github.com/fil-forge/ucantone/did"
	"github.com/fil-forge/ucantone/ipld/datamodel"
	"github.com/fil-forge/ucantone/server"
	"github.com/fil-forge/ucantone/ucan"
	"github.com/fil-forge/ucantone/ucan/command"
	"github.com/fil-forge/ucantone/ucan/container"
	"github.com/fil-forge/ucantone/ucan/delegation"

	delegation_store "github.com/fil-forge/sprue/pkg/store/delegation"
)

func NewAccessConfirmHandler(id identity.Identity, delegationStore delegation_store.Store, logger *zap.Logger) server.Route {
	log := logger.With(zap.Stringer("handler", access.Confirm))
	return access.Confirm.Route(
		func(req *binding.Request[*access.ConfirmArguments], res *binding.Response[*access.ConfirmOK]) error {
			args := req.Task().Arguments()
			if req.Invocation().Subject() != id.DID() {
				log.Warn("not a valid invocation", zap.Stringer("subject", req.Invocation().Subject()))
				return res.SetFailure(access.ErrInvalidAccessConfirmSubject)
			}

			accountDID, err := didmailto.Parse(args.Issuer.String())
			if err != nil {
				log.Warn("invalid issuer DID", zap.Stringer("issuer", args.Issuer), zap.Error(err))
				return res.SetFailure(access.ErrInvalidAccessConfirmIssuer)
			}

			account := attestation.Attest(req.Context(), accountDID, id)
			agent := args.Audience

			cmds := make([]string, 0, len(args.Attenuations))
			for _, att := range args.Attenuations {
				cmds = append(cmds, att.Command.String())
			}

			log := log.With(
				zap.Stringer("agent", agent),
				zap.Stringer("account", account),
				zap.Stringer("cause", args.Cause),
				zap.Strings("commands", cmds),
			)
			log.Debug("confirming access")

			// Create session proofs
			delegations, _, err := createSessionProofs(
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
			tokens := make([]ucan.Token, 0, len(delegations))
			for _, d := range delegations {
				tokens = append(tokens, d)
				links = append(links, d.Link())
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

			// Include the delegations in the response metadata.
			res.SetMetadata(container.New(
				container.WithDelegations(delegations...),
			))

			return res.SetSuccess(&access.ConfirmOK{Delegations: links})
		},
	)
}

// createSessionProofs creates delegations from the account to the agent.
func createSessionProofs(
	account ucan.Issuer,
	agent did.DID,
	attenuations []access.CapabilityRequest,
	meta datamodel.Map,
) ([]ucan.Delegation, []ucan.Invocation, error) {
	delegations := make([]ucan.Delegation, 0, len(attenuations))
	attestations := make([]ucan.Invocation, 0, len(attenuations))

	// Explicitly grant ability to operate as the account (subject = account).
	// This allows the agent to claim delegations that have the account as the
	// subject and also provision spaces with the account as the owner.
	accountDlg, err := delegation.Delegate(
		account,
		agent,
		account.DID(),
		command.Top(),
		delegation.WithMetadata(meta),
		delegation.WithNoExpiration(),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("creating delegation: %w", err)
	}
	delegations = append(delegations, accountDlg)

	for _, req := range attenuations {
		dlg, err := delegation.Delegate(
			account,
			agent,
			// TODO: optionally set subject in capability request
			// no subject (powerline) will apply to all spaces present and future
			did.Undef,
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
	}

	return delegations, attestations, nil
}
