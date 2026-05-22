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
	signer  ucan.Signer
	client  *client.HTTPClient
	logger  *zap.Logger
}

// New creates a new Piri client.
// The delegationFetcher is used to fetch delegation proofs on-demand for each request.
func New(endpoint *url.URL, piriDID did.DID, signer ucan.Signer, logger *zap.Logger) (*Client, error) {
	client, err := client.NewHTTP(endpoint)
	if err != nil {
		return nil, fmt.Errorf("creating HTTP client: %w", err)
	}
	return NewWithClient(piriDID, signer, client, logger), nil
}

func NewWithClient(piriDID did.DID, signer ucan.Signer, client *client.HTTPClient, logger *zap.Logger) *Client {
	return &Client{
		piriDID: piriDID,
		signer:  signer,
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
	inv, prfs, attestations, err := c.AllocateInvocation(ctx, req, proofStore, options...)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("creating allocate invocation: %w", err)
	}

	c.logger.Debug("ALLOCATE invocation created",
		zap.Stringer("issuer", inv.Issuer()),
		zap.Stringer("audience", inv.Audience()),
		zap.Int("proofs", len(prfs)),
		zap.Int("attestations", len(attestations)),
	)

	allocOK, rcpt, _, err := ucan_client.Execute[*blobcmds.AllocateOK](
		ctx,
		c.client,
		c.logger,
		inv,
		execution.WithDelegations(prfs...),
		execution.WithInvocations(attestations...),
	)
	if err != nil {
		return nil, nil, nil, err
	}
	return allocOK, inv, rcpt, nil
}

// AllocateInvocation returns the invocation for the allocate request (for use in effects).
func (c *Client) AllocateInvocation(ctx context.Context, req *AllocateRequest, proofStore ucanlib.ProofStore, options ...invocation.Option) (ucan.Invocation, []ucan.Delegation, []ucan.Invocation, error) {
	prfs, prfLinks, err := proofStore.ProofChain(ctx, c.signer.DID(), blobcmds.Allocate.Command, req.Space)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("building proof chain: %w", err)
	}

	attestations, err := proofStore.ProofAttestations(ctx, prfs, c.signer.DID())
	if err != nil {
		return nil, nil, nil, fmt.Errorf("getting proof attestations: %w", err)
	}

	options = slices.Clone(options)
	options = append(
		options,
		invocation.WithAudience(c.piriDID),
		invocation.WithProofs(prfLinks...),
	)

	inv, err := blobcmds.Allocate.Invoke(
		c.signer,
		req.Space,
		&blobcmds.AllocateArguments{
			Blob:  blobcmds.Blob{Digest: req.Digest, Size: req.Size},
			Cause: req.Cause,
		},
		options...,
	)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("creating allocate invocation: %w", err)
	}

	return inv, prfs, attestations, nil
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
	inv, prfs, attestations, err := c.AcceptInvocation(ctx, req, proofStore, options...)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("creating accept invocation: %w", err)
	}

	c.logger.Debug("ACCEPT invocation created",
		zap.Stringer("issuer", inv.Issuer()),
		zap.Stringer("audience", inv.Audience()),
		zap.Int("proofs", len(prfs)),
		zap.Int("attestations", len(attestations)),
	)

	acceptOK, rcpt, meta, err := ucan_client.Execute[*blobcmds.AcceptOK](
		ctx,
		c.client,
		c.logger,
		inv,
		execution.WithDelegations(prfs...),
		execution.WithInvocations(attestations...),
	)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	return acceptOK, inv, rcpt, meta, nil
}

// AcceptInvocation returns the invocation for the accept request (for use in effects).
func (c *Client) AcceptInvocation(ctx context.Context, req *AcceptRequest, proofStore ucanlib.ProofStore, options ...invocation.Option) (ucan.Invocation, []ucan.Delegation, []ucan.Invocation, error) {
	prfs, prfLinks, err := proofStore.ProofChain(ctx, c.signer.DID(), blobcmds.Accept.Command, req.Space)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("building proof chain: %w", err)
	}

	attestations, err := proofStore.ProofAttestations(ctx, prfs, c.signer.DID())
	if err != nil {
		return nil, nil, nil, fmt.Errorf("getting proof attestations: %w", err)
	}

	options = slices.Clone(options)
	options = append(
		options,
		invocation.WithAudience(c.piriDID),
		invocation.WithProofs(prfLinks...),
	)

	inv, err := blobcmds.Accept.Invoke(
		c.signer,
		req.Space,
		&blobcmds.AcceptArguments{
			Blob: blobcmds.Blob{Digest: req.Digest, Size: req.Size},
			Put:  promise.AwaitOK{Task: req.Put},
		},
		options...,
	)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("creating accept invocation: %w", err)
	}

	return inv, prfs, attestations, nil
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
	prfs, prfLinks, err := proofStore.ProofChain(ctx, c.signer.DID(), blobreplicacmds.Allocate.Command, req.Space)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("building proof chain: %w", err)
	}

	attestations, err := proofStore.ProofAttestations(ctx, prfs, c.signer.DID())
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("getting proof attestations: %w", err)
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
		c.signer,
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
		execution.WithInvocations(attestations...),
	)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	return allocOK, inv, rcpt, meta, nil
}
