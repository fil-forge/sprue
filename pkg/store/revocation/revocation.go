package revocation

import (
	"context"

	"github.com/fil-forge/ucantone/did"
	"github.com/ipfs/go-cid"
)

type Store interface {
	// Given a list of delegation CIDs, find all revocations for them, returning
	// a map of `ucan/revoke` invocation CIDs that caused the revocation, keyed
	// by scope DID (DID of the authority that issued the revocation - either the
	// issuer or audience of the revoked delegation or one of it's proofs).
	Find(ctx context.Context, delegations []cid.Cid) (map[cid.Cid]map[did.DID]cid.Cid, error)
	// Add a revocation for the given delegation to the revocation store. If there
	// is a revocation for a delegation with a _different_ scope, the revocation
	// with the given scope will be added. If there is a revocation for the given
	// delegation and scope no revocation will be added or updated.
	//
	// The scope is the DID of the authority that issued the revocation - either
	// the issuer or audience of the revoked delegation or one of it's proofs. The
	// cause is the CID of the `ucan/revoke` invocation that caused the revocation.
	Add(ctx context.Context, delegation cid.Cid, scope did.DID, cause cid.Cid) error
	// Creates or updates the revocation for given delegation by setting scope to
	// the one passed in the argument. This is intended to compact the revocation
	// store by dropping all existing revocations for the given delegation in
	// favor of the given one. It is supposed to be called when the revocation
	// authority is the same as the UCAN issuer, as such a revocation will apply
	// to all possible invocations.
	Reset(ctx context.Context, delegation cid.Cid, scope did.DID, cause cid.Cid) error
}
