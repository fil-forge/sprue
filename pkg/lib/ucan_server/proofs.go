package ucan_server

import (
	"context"
	"iter"

	ucanlib "github.com/fil-forge/libforge/ucan"
	"github.com/fil-forge/ucantone/did"
	"github.com/fil-forge/ucantone/ucan"
	"github.com/ipfs/go-cid"
)

type ProofStore interface {
	ProofChain(ctx context.Context, aud did.DID, cmd ucan.Command, sub did.DID) ([]ucan.Delegation, []cid.Cid, error)
	ProofAttestations(ctx context.Context, proofs []ucan.Delegation, authority did.DID) ([]ucan.Invocation, error)
}

// ContainerProofStore is a proof store backed by an in-memory container.
type ContainerProofStore struct {
	container ucan.Container
}

// NewContainerProofStore creates a proof store backed by an in-memory container.
func NewContainerProofStore(ct ucan.Container) *ContainerProofStore {
	return &ContainerProofStore{container: ct}
}

func (cps *ContainerProofStore) ProofChain(ctx context.Context, aud did.DID, cmd ucan.Command, sub did.DID) ([]ucan.Delegation, []cid.Cid, error) {
	return ucanlib.ProofChain(ctx, cps.matchDelegations, aud, cmd, sub)
}

func (cps *ContainerProofStore) ProofAttestations(ctx context.Context, proofs []ucan.Delegation, authority did.DID) ([]ucan.Invocation, error) {
	return ucanlib.ProofAttestations(ctx, cps.listInvocations, proofs, authority)
}

func (ps *ContainerProofStore) listDelegations(ctx context.Context, aud did.DID, cmd ucan.Command, sub did.DID) iter.Seq2[ucan.Delegation, error] {
	return func(yield func(ucan.Delegation, error) bool) {
		if ps.container == nil {
			return
		}
		for _, d := range ps.container.Delegations() {
			if d.Audience() == aud && d.Command() == cmd && d.Subject() == sub {
				if !yield(d, nil) {
					return
				}
			}
		}
	}
}

func (ps *ContainerProofStore) matchDelegations(ctx context.Context, aud did.DID, cmd ucan.Command, sub did.DID) iter.Seq2[ucan.Delegation, error] {
	return ucanlib.NewDelegationMatcher(ps.listDelegations)(ctx, aud, cmd, sub)
}

func (ps *ContainerProofStore) listInvocations(ctx context.Context, aud did.DID, cmd ucan.Command, sub did.DID) iter.Seq2[ucan.Invocation, error] {
	return func(yield func(ucan.Invocation, error) bool) {
		if ps.container == nil {
			return
		}
		for _, d := range ps.container.Invocations() {
			if d.Audience() == aud && d.Command() == cmd && d.Subject() == sub {
				if !yield(d, nil) {
					return
				}
			}
		}
	}
}
