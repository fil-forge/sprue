package ucan_server

import (
	"context"
	"iter"

	ucanlib "github.com/fil-forge/libforge/ucan"
	"github.com/fil-forge/ucantone/ucan"
)

type ProofStore interface {
	ProofChain(ctx context.Context, aud ucan.Principal, cmd ucan.Command, sub ucan.Principal) ([]ucan.Delegation, []ucan.Link, error)
	ProofAttestations(ctx context.Context, proofs []ucan.Delegation, authority ucan.Principal) ([]ucan.Invocation, error)
}

// ContainerProofStore is a proof store backed by an in-memory container.
type ContainerProofStore struct {
	container ucan.Container
}

// NewContainerProofStore creates a proof store backed by an in-memory container.
func NewContainerProofStore(ct ucan.Container) *ContainerProofStore {
	return &ContainerProofStore{container: ct}
}

func (cps *ContainerProofStore) ProofChain(ctx context.Context, aud ucan.Principal, cmd ucan.Command, sub ucan.Principal) ([]ucan.Delegation, []ucan.Link, error) {
	return ucanlib.ProofChain(ctx, cps.matchDelegations, aud, cmd, sub)
}

func (cps *ContainerProofStore) ProofAttestations(ctx context.Context, proofs []ucan.Delegation, authority ucan.Principal) ([]ucan.Invocation, error) {
	return ucanlib.ProofAttestations(ctx, cps.listInvocations, proofs, authority)
}

func (ps *ContainerProofStore) listDelegations(ctx context.Context, aud ucan.Principal, cmd ucan.Command, sub ucan.Subject) iter.Seq2[ucan.Delegation, error] {
	return func(yield func(ucan.Delegation, error) bool) {
		if ps.container == nil {
			return
		}
		for _, d := range ps.container.Delegations() {
			if d.Audience().DID() == aud.DID() && d.Command() == cmd && equalSubject(d.Subject(), sub) {
				if !yield(d, nil) {
					return
				}
			}
		}
	}
}

func (ps *ContainerProofStore) matchDelegations(ctx context.Context, aud ucan.Principal, cmd ucan.Command, sub ucan.Subject) iter.Seq2[ucan.Delegation, error] {
	return ucanlib.NewDelegationMatcher(ps.listDelegations)(ctx, aud, cmd, sub)
}

func (ps *ContainerProofStore) listInvocations(ctx context.Context, aud ucan.Principal, cmd ucan.Command, sub ucan.Subject) iter.Seq2[ucan.Invocation, error] {
	return func(yield func(ucan.Invocation, error) bool) {
		if ps.container == nil {
			return
		}
		for _, d := range ps.container.Invocations() {
			if d.Audience().DID() == aud.DID() && d.Command() == cmd && equalSubject(d.Subject(), sub) {
				if !yield(d, nil) {
					return
				}
			}
		}
	}
}

func equalSubject(a, b ucan.Subject) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.DID() == b.DID()
}
