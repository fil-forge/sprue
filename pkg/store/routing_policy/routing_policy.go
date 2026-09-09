// Package routingpolicy stores routing policies (a policy DID with its set of
// storage node candidates) and the policy each space references.
package routingpolicy

import (
	"context"
	"time"

	"github.com/fil-forge/ucantone/did"
	"github.com/fil-forge/ucantone/errors"
	"github.com/ipfs/go-cid"
)

const (
	// PolicyNotFoundErrorName is the name given to an error where the routing
	// policy is not found in the store.
	PolicyNotFoundErrorName = "RoutingPolicyNotFound"
	// SpacePolicyNotFoundErrorName is the name given to an error where the
	// space has no routing policy reference.
	SpacePolicyNotFoundErrorName = "SpaceRoutingPolicyNotFound"
)

var (
	ErrPolicyNotFound      = errors.New(PolicyNotFoundErrorName, "routing policy not found")
	ErrSpacePolicyNotFound = errors.New(SpacePolicyNotFoundErrorName, "space has no routing policy")
)

// PolicyRecord is a routing policy and its candidate storage nodes.
type PolicyRecord struct {
	// DID of the routing policy.
	Policy did.DID
	// Candidates are the storage nodes writes may be routed to.
	Candidates []did.DID
	// Cause is the CID of the task for the invocation that last set the
	// candidates.
	Cause cid.Cid
	// Date and time the record was created (ISO 8601).
	InsertedAt time.Time
	// Date and time the record was last updated (ISO 8601).
	UpdatedAt time.Time
}

// SpaceRecord is the routing policy reference of a space.
type SpaceRecord struct {
	// DID of the space.
	Space did.DID
	// DID of the routing policy the space references.
	Policy did.DID
	// Cause is the CID of the task for the invocation that last set the
	// reference.
	Cause cid.Cid
	// Date and time the record was created (ISO 8601).
	InsertedAt time.Time
	// Date and time the record was last updated (ISO 8601).
	UpdatedAt time.Time
}

type Store interface {
	// SetCandidates replaces the candidate set of a policy, creating the policy
	// if it does not exist. The candidate set must not be empty. Cause is the
	// CID of the task for the invocation making the change.
	SetCandidates(ctx context.Context, policy did.DID, candidates []did.DID, cause cid.Cid) error
	// GetPolicy returns a policy record. May return [ErrPolicyNotFound].
	GetPolicy(ctx context.Context, policy did.DID) (PolicyRecord, error)
	// SetSpacePolicy sets the policy a space references, replacing any existing
	// reference. Cause is the CID of the task for the invocation making the
	// change. May return [ErrPolicyNotFound] if the policy does not exist.
	SetSpacePolicy(ctx context.Context, space did.DID, policy did.DID, cause cid.Cid) error
	// ClearSpacePolicy removes the policy reference of a space. It succeeds if
	// the space has no reference.
	ClearSpacePolicy(ctx context.Context, space did.DID) error
	// GetSpacePolicy returns the policy reference of a space. May return
	// [ErrSpacePolicyNotFound].
	GetSpacePolicy(ctx context.Context, space did.DID) (SpaceRecord, error)
}
