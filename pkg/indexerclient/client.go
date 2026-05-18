package indexerclient

import (
	"context"
	"fmt"
	"net/url"

	assertcaps "github.com/fil-forge/libforge/capabilities/assert"
	contentcaps "github.com/fil-forge/libforge/capabilities/content"
	"github.com/fil-forge/sprue/pkg/lib/ucan_client"
	"github.com/fil-forge/sprue/pkg/lib/ucan_server"
	"github.com/fil-forge/ucantone/client"
	"github.com/fil-forge/ucantone/did"
	"github.com/fil-forge/ucantone/execution"
	"github.com/fil-forge/ucantone/ipld/datamodel"
	"github.com/fil-forge/ucantone/ucan"
	"github.com/fil-forge/ucantone/ucan/invocation"
	"github.com/ipfs/go-cid"
	"go.uber.org/zap"
)

// Client is a UCAN client for communicating with the indexer service.
type Client struct {
	endpoint   *url.URL
	indexerDID did.DID
	signer     ucan.Signer
	client     *client.HTTPClient
	logger     *zap.Logger
}

// New creates a new indexer client.
func New(endpoint *url.URL, indexerDID did.DID, signer ucan.Signer, logger *zap.Logger) (*Client, error) {
	client, err := client.NewHTTP(endpoint)
	if err != nil {
		return nil, fmt.Errorf("creating HTTP client: %w", err)
	}
	return &Client{
		endpoint:   endpoint,
		indexerDID: indexerDID,
		signer:     signer,
		client:     client,
		logger:     logger,
	}, nil
}

// PublishIndexClaim sends an /assert/index claim to the indexer.
//
// The proofStore parameter is used to build the delegation chain authorizing
// the upload service to retrieve the index blob via `/content/retrieve` command.
func (c *Client) PublishIndexClaim(ctx context.Context, space did.DID, index cid.Cid, proofStore ucan_server.ProofStore, options ...invocation.Option) (ucan.Receipt, error) {
	prfs, prfLinks, err := proofStore.ProofChain(ctx, c.signer.DID(), contentcaps.RetrieveCommand, space)
	if err != nil {
		return nil, fmt.Errorf("building proof chain: %w", err)
	}
	attestations, err := proofStore.ProofAttestations(ctx, prfs, c.signer.DID())
	if err != nil {
		return nil, fmt.Errorf("building attestations: %w", err)
	}
	// Create a content retrieval delegation from upload service to indexer
	indexerDelegation, err := contentcaps.Retrieve.Delegate(c.signer, c.indexerDID, space)
	if err != nil {
		return nil, fmt.Errorf("creating indexer delegation: %w", err)
	}

	inv, err := assertcaps.Index.Invoke(
		c.signer,
		c.signer.DID(),
		&assertcaps.IndexArguments{Index: index},
		invocation.WithAudience(c.indexerDID),
		invocation.WithMetadata(
			datamodel.Map{"retrievalAuth": append(prfLinks, indexerDelegation.Link())},
		),
	)
	if err != nil {
		return nil, fmt.Errorf("creating invocation: %w", err)
	}

	_, rcpt, err := ucan_client.Execute[*assertcaps.IndexOK](
		ctx,
		c.client,
		c.logger,
		inv,
		execution.WithDelegations(prfs...),
		execution.WithDelegations(indexerDelegation),
		execution.WithInvocations(attestations...),
	)
	if err != nil {
		return nil, fmt.Errorf("executing assert index invocation: %w", err)
	}
	return rcpt, nil
}
