package piriclient

import (
	"context"
	"fmt"
	"net/url"
	"slices"
	"time"

	blobcmds "github.com/fil-forge/libforge/commands/blob"
	blobreplicacmds "github.com/fil-forge/libforge/commands/blob/replica"
	ucanlib "github.com/fil-forge/libforge/ucan"
	"github.com/fil-forge/sprue/pkg/lib/ucan_client"
	"github.com/fil-forge/ucantone/client"
	"github.com/fil-forge/ucantone/did"
	"github.com/fil-forge/ucantone/execution"
	"github.com/fil-forge/ucantone/ucan"
	"github.com/fil-forge/ucantone/ucan/invocation"
	"github.com/fil-forge/ucantone/ucan/promise"
	"github.com/ipfs/go-cid"
	"go.uber.org/zap"
)

// Replication invocation timeout.
//
// Note: we set a reasonably large expiration as replication nodes use the
// invocation as proof for obtaining a retrieval delegation, and we want to
// allow for retries and/or job queue delays.
const replicaAllocationTTL = time.Hour * 24

// Client is a UCAN client for communicating with Piri nodes.
type Client struct {
	piriDID did.DID
	issuer  ucan.Issuer
	client  *client.HTTPClient
	logger  *zap.Logger
}

// New creates a new Piri client.
// The delegationFetcher is used to fetch delegation proofs on-demand for each request.
func New(endpoint *url.URL, piriDID did.DID, issuer ucan.Issuer, logger *zap.Logger) (*Client, error) {
	client, err := client.NewHTTP(endpoint)
	if err != nil {
		return nil, fmt.Errorf("creating HTTP client: %w", err)
	}
	return NewWithClient(piriDID, issuer, client, logger), nil
}

func NewWithClient(piriDID did.DID, issuer ucan.Issuer, client *client.HTTPClient, logger *zap.Logger) *Client {
	return &Client{
		piriDID: piriDID,
		issuer:  issuer,
		client:  client,
		logger:  logger,
	}
}

// AllocateRequest contains the parameters for a /blob/allocate invocation.
type AllocateRequest struct {
	Space  did.DID
	Digest []byte
	Size   uint64
	Cause  cid.Cid
}

// Allocate sends a /blob/allocate invocation to the piri node.
// Returns the response data, the invocation that was sent, and the receipt from piri.
func (c *Client) Allocate(ctx context.Context, req *AllocateRequest, proofStore ucanlib.ProofStore, options ...invocation.Option) (*blobcmds.AllocateOK, ucan.Invocation, ucan.Receipt, error) {
	inv, prfs, err := c.AllocateInvocation(ctx, req, proofStore, options...)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("creating allocate invocation: %w", err)
	}

	c.logger.Debug("ALLOCATE invocation created",
		zap.Stringer("issuer", inv.Issuer()),
		zap.Stringer("audience", inv.Audience()),
		zap.Int("proofs", len(prfs)),
	)

	allocOK, rcpt, _, err := ucan_client.Execute[*blobcmds.AllocateOK](
		ctx,
		c.client,
		c.logger,
		inv,
		execution.WithDelegations(prfs...),
	)
	if err != nil {
		return nil, nil, nil, err
	}
	return allocOK, inv, rcpt, nil
}

// AllocateInvocation returns the invocation for the allocate request (for use in effects).
func (c *Client) AllocateInvocation(ctx context.Context, req *AllocateRequest, proofStore ucanlib.ProofStore, options ...invocation.Option) (ucan.Invocation, []ucan.Delegation, error) {
	// The proof chain is rooted at the storage provider (the proofs the provider
	// granted the upload service at registration), so the subject is the provider
	// DID, not the space. The space rides in the invocation arguments instead.
	prfs, prfLinks, err := proofStore.ProofChain(ctx, c.issuer.DID(), blobcmds.Allocate.Command, c.piriDID)
	if err != nil {
		return nil, nil, fmt.Errorf("building proof chain: %w", err)
	}

	options = slices.Clone(options)
	options = append(
		options,
		invocation.WithAudience(c.piriDID),
		invocation.WithProofs(prfLinks...),
	)

	inv, err := blobcmds.Allocate.Invoke(
		c.issuer,
		c.piriDID,
		&blobcmds.AllocateArguments{
			Space: req.Space,
			Blob:  blobcmds.Blob{Digest: req.Digest, Size: req.Size},
			Cause: req.Cause,
		},
		options...,
	)
	if err != nil {
		return nil, nil, fmt.Errorf("creating allocate invocation: %w", err)
	}

	return inv, prfs, nil
}

// PiriDID returns the DID of the piri node.
func (c *Client) PiriDID() did.DID {
	return c.piriDID
}

// AcceptRequest contains the parameters for a /blob/accept invocation.
type AcceptRequest struct {
	Space  did.DID
	Digest []byte
	Size   uint64
	Put    cid.Cid // Link to the /http/put task that uploaded the blob
}

// Accept sends a /blob/accept invocation to the piri node.
func (c *Client) Accept(ctx context.Context, req *AcceptRequest, proofStore ucanlib.ProofStore, options ...invocation.Option) (*blobcmds.AcceptOK, ucan.Invocation, ucan.Receipt, ucan.Container, error) {
	inv, prfs, err := c.AcceptInvocation(ctx, req, proofStore, options...)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("creating accept invocation: %w", err)
	}

	c.logger.Debug("ACCEPT invocation created",
		zap.Stringer("issuer", inv.Issuer()),
		zap.Stringer("audience", inv.Audience()),
		zap.Int("proofs", len(prfs)),
	)

	acceptOK, rcpt, meta, err := ucan_client.Execute[*blobcmds.AcceptOK](
		ctx,
		c.client,
		c.logger,
		inv,
		execution.WithDelegations(prfs...),
	)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	return acceptOK, inv, rcpt, meta, nil
}

// AcceptInvocation returns the invocation for the accept request (for use in effects).
func (c *Client) AcceptInvocation(ctx context.Context, req *AcceptRequest, proofStore ucanlib.ProofStore, options ...invocation.Option) (ucan.Invocation, []ucan.Delegation, error) {
	// As with allocate, the proof chain is rooted at the storage provider, so the
	// subject is the provider DID and the space travels in the arguments.
	prfs, prfLinks, err := proofStore.ProofChain(ctx, c.issuer.DID(), blobcmds.Accept.Command, c.piriDID)
	if err != nil {
		return nil, nil, fmt.Errorf("building proof chain: %w", err)
	}

	options = slices.Clone(options)
	options = append(
		options,
		invocation.WithAudience(c.piriDID),
		invocation.WithProofs(prfLinks...),
	)

	inv, err := blobcmds.Accept.Invoke(
		c.issuer,
		c.piriDID,
		&blobcmds.AcceptArguments{
			Space: req.Space,
			Blob:  blobcmds.Blob{Digest: req.Digest, Size: req.Size},
			Put:   promise.AwaitOK{Task: req.Put},
		},
		options...,
	)
	if err != nil {
		return nil, nil, fmt.Errorf("creating accept invocation: %w", err)
	}

	return inv, prfs, nil
}

// RemoveRequest contains the parameters for a /blob/remove invocation.
type RemoveRequest struct {
	Space  did.DID
	Digest []byte
}

// Remove sends a /blob/remove invocation to the piri node, releasing the
// space's claim on the blob. Returns the response data, the invocation that
// was sent, and the receipt from piri. Piri's handler is idempotent, so
// removing an already-removed blob succeeds.
func (c *Client) Remove(ctx context.Context, req *RemoveRequest, proofStore ucanlib.ProofStore, options ...invocation.Option) (*blobcmds.RemoveOK, ucan.Invocation, ucan.Receipt, error) {
	inv, prfs, err := c.RemoveInvocation(ctx, req, proofStore, options...)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("creating remove invocation: %w", err)
	}

	c.logger.Debug("REMOVE invocation created",
		zap.Stringer("issuer", inv.Issuer()),
		zap.Stringer("audience", inv.Audience()),
		zap.Int("proofs", len(prfs)),
	)

	removeOK, rcpt, _, err := ucan_client.Execute[*blobcmds.RemoveOK](
		ctx,
		c.client,
		c.logger,
		inv,
		execution.WithDelegations(prfs...),
	)
	if err != nil {
		return nil, nil, nil, err
	}
	return removeOK, inv, rcpt, nil
}

// RemoveInvocation returns the invocation for the remove request.
func (c *Client) RemoveInvocation(ctx context.Context, req *RemoveRequest, proofStore ucanlib.ProofStore, options ...invocation.Option) (ucan.Invocation, []ucan.Delegation, error) {
	// As with allocate/accept, the proof chain is rooted at the storage
	// provider, so the subject is the provider DID and the space travels in
	// the arguments.
	prfs, prfLinks, err := proofStore.ProofChain(ctx, c.issuer.DID(), blobcmds.Remove.Command, c.piriDID)
	if err != nil {
		return nil, nil, fmt.Errorf("building proof chain: %w", err)
	}

	options = slices.Clone(options)
	options = append(
		options,
		invocation.WithAudience(c.piriDID),
		invocation.WithProofs(prfLinks...),
	)

	inv, err := blobcmds.Remove.Invoke(
		c.issuer,
		c.piriDID,
		&blobcmds.RemoveArguments{
			Space:  req.Space,
			Digest: req.Digest,
		},
		options...,
	)
	if err != nil {
		return nil, nil, fmt.Errorf("creating remove invocation: %w", err)
	}

	return inv, prfs, nil
}

// RejectRequest contains the parameters for a /blob/reject invocation.
type RejectRequest struct {
	Space  did.DID
	Digest []byte
}

// Reject sends a /blob/reject invocation to the piri node, retiring the
// space's parked (never-accepted) blob. Piri refuses accepted blobs with a
// BlobAccepted failure; otherwise the handler is idempotent.
func (c *Client) Reject(ctx context.Context, req *RejectRequest, proofStore ucanlib.ProofStore, options ...invocation.Option) (*blobcmds.RejectOK, ucan.Invocation, ucan.Receipt, error) {
	inv, prfs, err := c.RejectInvocation(ctx, req, proofStore, options...)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("creating reject invocation: %w", err)
	}

	c.logger.Debug("REJECT invocation created",
		zap.Stringer("issuer", inv.Issuer()),
		zap.Stringer("audience", inv.Audience()),
		zap.Int("proofs", len(prfs)),
	)

	rejectOK, rcpt, _, err := ucan_client.Execute[*blobcmds.RejectOK](
		ctx,
		c.client,
		c.logger,
		inv,
		execution.WithDelegations(prfs...),
	)
	if err != nil {
		return nil, nil, nil, err
	}
	return rejectOK, inv, rcpt, nil
}

// RejectInvocation returns the invocation for the reject request.
func (c *Client) RejectInvocation(ctx context.Context, req *RejectRequest, proofStore ucanlib.ProofStore, options ...invocation.Option) (ucan.Invocation, []ucan.Delegation, error) {
	// As with allocate/accept/remove, the proof chain is rooted at the
	// storage provider, so the subject is the provider DID and the space
	// travels in the arguments. Cause is not forwarded — it is upload-service
	// routing metadata, meaningless to the node.
	prfs, prfLinks, err := proofStore.ProofChain(ctx, c.issuer.DID(), blobcmds.Reject.Command, c.piriDID)
	if err != nil {
		return nil, nil, fmt.Errorf("building proof chain: %w", err)
	}

	options = slices.Clone(options)
	options = append(
		options,
		invocation.WithAudience(c.piriDID),
		invocation.WithProofs(prfLinks...),
	)

	inv, err := blobcmds.Reject.Invoke(
		c.issuer,
		c.piriDID,
		&blobcmds.RejectArguments{
			Space:  req.Space,
			Digest: req.Digest,
		},
		options...,
	)
	if err != nil {
		return nil, nil, fmt.Errorf("creating reject invocation: %w", err)
	}

	return inv, prfs, nil
}

// ReplicaAllocateRequest contains the parameters for a /blob/replica/allocate invocation.
type ReplicaAllocateRequest struct {
	Space  did.DID
	Digest []byte
	Size   uint64
	Site   ucan.Invocation // Location commitment
	Cause  cid.Cid
}

// ReplicaAllocate sends a /blob/replica/allocate invocation to the piri node.
// Returns the response data, the invocation that was sent, the receipt from
// piri, and any metadata. It returns an error if the receipt contains a failure result.
func (c *Client) ReplicaAllocate(ctx context.Context, req *ReplicaAllocateRequest, proofStore ucanlib.ProofStore, options ...invocation.Option) (*blobreplicacmds.AllocateOK, ucan.Invocation, ucan.Receipt, ucan.Container, error) {
	prfs, prfLinks, err := proofStore.ProofChain(ctx, c.issuer.DID(), blobreplicacmds.Allocate.Command, req.Space)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("building proof chain: %w", err)
	}

	options = slices.Clone(options)
	options = append(
		options,
		invocation.WithAudience(c.piriDID),
		invocation.WithProofs(prfLinks...),
		// We set a reasonably large expiration as replication nodes use the
		// invocation as proof for obtaining a retrieval delegation, and we want to
		// allow for retries and/or job queue delays.
		invocation.WithExpiration(ucan.UnixTimestamp(time.Now().Add(replicaAllocationTTL).Unix())),
	)

	inv, err := blobreplicacmds.Allocate.Invoke(
		c.issuer,
		req.Space,
		&blobreplicacmds.AllocateArguments{
			Blob:  blobcmds.Blob{Digest: req.Digest, Size: req.Size},
			Site:  req.Site.Link(),
			Cause: req.Cause,
		},
		options...,
	)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("creating replica allocate invocation: %w", err)
	}

	c.logger.Debug("REPLICA ALLOCATE invocation created",
		zap.Stringer("issuer", inv.Issuer()),
		zap.Stringer("audience", inv.Audience()),
		zap.Int("proofs", len(inv.Proofs())))

	allocOK, rcpt, meta, err := ucan_client.Execute[*blobreplicacmds.AllocateOK](
		ctx,
		c.client,
		c.logger,
		inv,
		execution.WithDelegations(prfs...),
	)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	return allocOK, inv, rcpt, meta, nil
}
