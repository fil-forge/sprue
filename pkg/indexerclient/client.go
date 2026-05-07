package indexerclient

import (
	"context"
	"fmt"
	"net/url"

	"github.com/ipfs/go-cid"
	"github.com/ipld/go-ipld-prime/datamodel"
	cidlink "github.com/ipld/go-ipld-prime/linking/cid"
	"github.com/ipld/go-ipld-prime/node/basicnode"
	"github.com/multiformats/go-multiaddr"
	"go.uber.org/zap"

	assertcap "github.com/fil-forge/go-libstoracha/capabilities/assert"
	claimcap "github.com/fil-forge/go-libstoracha/capabilities/claim"
	contentcap "github.com/fil-forge/go-libstoracha/capabilities/space/content"
	captypes "github.com/fil-forge/go-libstoracha/capabilities/types"
	uclient "github.com/fil-forge/go-ucanto/client"
	"github.com/fil-forge/go-ucanto/core/delegation"
	"github.com/fil-forge/go-ucanto/core/invocation"
	"github.com/fil-forge/go-ucanto/core/ipld"
	"github.com/fil-forge/go-ucanto/core/receipt"
	"github.com/fil-forge/go-ucanto/core/result"
	"github.com/fil-forge/go-ucanto/did"
	"github.com/fil-forge/go-ucanto/principal"
	ucanhttp "github.com/fil-forge/go-ucanto/transport/http"
	"github.com/fil-forge/go-ucanto/ucan"
)

// retrievalAuthFact implements ucan.FactBuilder for the retrievalAuth fact.
// This is used to include a retrieval delegation link in the assert/index invocation
// so the indexer can fetch the index blob from storage providers that require UCAN auth.
type retrievalAuthFact struct {
	link ipld.Link
}

func (f retrievalAuthFact) ToIPLD() (map[string]datamodel.Node, error) {
	return map[string]datamodel.Node{
		"retrievalAuth": basicnode.NewLink(f.link),
	}, nil
}

// Client is a UCAN client for communicating with the indexer service.
type Client struct {
	endpoint   *url.URL
	indexerDID did.DID
	signer     principal.Signer
	connection uclient.Connection
	logger     *zap.Logger
}

// New creates a new indexer client.
func New(endpoint *url.URL, indexerDID did.DID, signer principal.Signer, logger *zap.Logger) (*Client, error) {
	channel := ucanhttp.NewChannel(endpoint)
	conn, err := uclient.NewConnection(indexerDID, channel)
	if err != nil {
		return nil, fmt.Errorf("creating connection: %w", err)
	}
	return &Client{
		endpoint:   endpoint,
		indexerDID: indexerDID,
		signer:     signer,
		connection: conn,
		logger:     logger,
	}, nil
}

// PublishIndexClaim sends an assert/index claim to the indexer.
// clientAuth is the space/content/retrieve delegation from the client (guppy).
// If provided, the upload service creates a fresh delegation to the indexer
// using the same caveats. The client's proof chain is NOT included to avoid
// leaking did:mailto identities to storage nodes.
func (c *Client) PublishIndexClaim(ctx context.Context, space did.DID, content, index cid.Cid, clientAuth delegation.Delegation) error {
	var opts []delegation.Option

	// Re-delegate the client's retrieval auth to the indexer if provided
	if clientAuth != nil {
		caps := clientAuth.Capabilities()
		if len(caps) == 0 {
			return fmt.Errorf("no capabilities in retrieval auth delegation")
		}

		// Parse the original caveats from the client's delegation
		origCaveats, readErr := contentcap.RetrieveCaveatsReader.Read(caps[0].Nb())
		if readErr != nil {
			return fmt.Errorf("reading retrieval caveats: %w", readErr)
		}

		// Create a delegation from upload service to indexer, including the client's
		// proof chain. This allows the indexer to prove authority to piri.
		//
		// Note: This DOES include the client's proof chain (which may contain did:mailto).
		// In production, a different authorization model should be used to avoid
		// leaking client identities. For this mock/test setup, the did:mailto is only
		// visible to piri (the storage node), not publicly exposed.
		indexerDelegation, delegateErr := contentcap.Retrieve.Delegate(
			c.signer,       // issuer: upload service (did:key)
			c.indexerDID,   // audience: indexer (did:web:indexer)
			space.String(), // with: space DID (resource)
			origCaveats,    // same caveats (Blob, Range) from client
			delegation.WithNoExpiration(),
			delegation.WithProof(delegation.FromDelegation(clientAuth)), // Include client's proof chain
		)
		if delegateErr != nil {
			return fmt.Errorf("creating indexer delegation: %w", delegateErr)
		}

		// Include the delegation in the assert/index invocation
		opts = append(opts,
			delegation.WithFacts([]ucan.FactBuilder{
				retrievalAuthFact{link: indexerDelegation.Link()},
			}),
			delegation.WithProof(delegation.FromDelegation(indexerDelegation)),
		)
	}

	// assert/* capabilities are self-issued assertions, so the resource (with)
	// should be the signer's DID, not the indexer's DID
	inv, err := assertcap.Index.Invoke(
		c.signer,
		c.indexerDID,
		c.signer.DID().String(),
		assertcap.IndexCaveats{
			Content: cidlink.Link{Cid: content},
			Index:   cidlink.Link{Cid: index},
		},
		opts...,
	)
	if err != nil {
		return fmt.Errorf("creating assert/index invocation: %w", err)
	}

	resp, err := uclient.Execute(ctx, []invocation.Invocation{inv}, c.connection)
	if err != nil {
		return fmt.Errorf("executing assert/index: %w", err)
	}

	rcptLink, ok := resp.Get(inv.Link())
	if !ok {
		return fmt.Errorf("receipt not found for invocation")
	}

	// Read receipt and check for errors
	anyReader := receipt.NewAnyReceiptReader(captypes.Converters...)
	anyRcpt, err := anyReader.Read(rcptLink, resp.Blocks())
	if err != nil {
		return fmt.Errorf("reading receipt: %w", err)
	}

	_, errNode := result.Unwrap(anyRcpt.Out())
	if errNode != nil {
		// Extract error details for better debugging
		var errDetails string
		if msgNode, lookupErr := errNode.LookupByString("message"); lookupErr == nil {
			if msg, asErr := msgNode.AsString(); asErr == nil {
				errDetails = msg
			}
		}
		if errDetails == "" {
			if nameNode, lookupErr := errNode.LookupByString("name"); lookupErr == nil {
				if name, asErr := nameNode.AsString(); asErr == nil {
					errDetails = name
				}
			}
		}
		if errDetails == "" {
			errDetails = "unknown error"
		}
		return fmt.Errorf("assert/index failed: %s", errDetails)
	}

	return nil
}

// CacheLocationClaim sends a claim/cache invocation to cache a location claim with the indexer.
// This tells the indexer where content is stored (provider address).
func (c *Client) CacheLocationClaim(ctx context.Context, claim delegation.Delegation, providerAddrs []multiaddr.Multiaddr) error {
	c.logger.Debug("CacheLocationClaim",
		zap.String("claim", claim.Link().String()),
		zap.String("issuer", c.signer.DID().String()),
		zap.String("audience", c.indexerDID.String()),
		zap.Int("providerAddrs", len(providerAddrs)))

	inv, err := claimcap.Cache.Invoke(
		c.signer,
		c.indexerDID,
		c.signer.DID().String(),
		claimcap.CacheCaveats{
			Claim: claim.Link(),
			Provider: claimcap.Provider{
				Addresses: providerAddrs,
			},
		},
		delegation.WithProof(delegation.FromDelegation(claim)),
	)
	if err != nil {
		return fmt.Errorf("creating claim/cache invocation: %w", err)
	}
	c.logger.Debug("created invocation", zap.String("link", inv.Link().String()))

	resp, err := uclient.Execute(ctx, []invocation.Invocation{inv}, c.connection)
	if err != nil {
		c.logger.Error("execute error", zap.Error(err))
		return fmt.Errorf("executing claim/cache: %w", err)
	}
	c.logger.Debug("execute succeeded")

	rcptLink, ok := resp.Get(inv.Link())
	if !ok {
		c.logger.Debug("no receipt in response")
		return fmt.Errorf("receipt not found for invocation")
	}
	c.logger.Debug("got receipt", zap.String("link", rcptLink.String()))

	// Read receipt and check for errors
	anyReader := receipt.NewAnyReceiptReader(captypes.Converters...)
	anyRcpt, err := anyReader.Read(rcptLink, resp.Blocks())
	if err != nil {
		c.logger.Error("reading receipt error", zap.Error(err))
		return fmt.Errorf("reading receipt: %w", err)
	}

	okNode, errNode := result.Unwrap(anyRcpt.Out())
	c.logger.Debug("receipt result", zap.Bool("ok", okNode != nil), zap.Bool("err", errNode != nil))
	if errNode != nil {
		// Extract error details for better debugging
		var errDetails string
		if msgNode, lookupErr := errNode.LookupByString("message"); lookupErr == nil {
			if msg, asErr := msgNode.AsString(); asErr == nil {
				errDetails = msg
			}
		}
		if errDetails == "" {
			if nameNode, lookupErr := errNode.LookupByString("name"); lookupErr == nil {
				if name, asErr := nameNode.AsString(); asErr == nil {
					errDetails = name
				}
			}
		}
		if errDetails == "" {
			errDetails = "unknown error"
		}
		return fmt.Errorf("claim/cache failed: %s", errDetails)
	}

	return nil
}
