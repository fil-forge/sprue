package client

import (
	"context"
	"fmt"
	"net/url"
	"slices"

	routingcmds "github.com/fil-forge/libforge/commands/routing"
	ucanlib "github.com/fil-forge/libforge/ucan"
	providercap "github.com/fil-forge/sprue/pkg/commands/admin/provider"
	weightcap "github.com/fil-forge/sprue/pkg/commands/admin/provider/weight"
	"github.com/fil-forge/sprue/pkg/lib/ucan_client"
	"github.com/fil-forge/ucantone/binding"
	"github.com/fil-forge/ucantone/client"
	"github.com/fil-forge/ucantone/did"
	"github.com/fil-forge/ucantone/execution"
	"github.com/fil-forge/ucantone/ucan"
	"github.com/fil-forge/ucantone/ucan/container"
	"github.com/fil-forge/ucantone/ucan/invocation"
	"go.uber.org/zap"
)

type Client struct {
	uploadServiceID did.DID
	client          *client.HTTPClient
	issuer          ucan.Issuer
	logger          *zap.Logger
}

func New(uploadServiceID did.DID, endpoint *url.URL, issuer ucan.Issuer, logger *zap.Logger) (*Client, error) {
	client, err := client.NewHTTP(endpoint)
	if err != nil {
		return nil, fmt.Errorf("creating HTTP client: %w", err)
	}
	return NewWithClient(uploadServiceID, client, issuer, logger), nil
}

func NewWithClient(uploadServiceID did.DID, client *client.HTTPClient, issuer ucan.Issuer, logger *zap.Logger) *Client {
	return &Client{
		uploadServiceID: uploadServiceID,
		issuer:          issuer,
		client:          client,
		logger:          logger,
	}
}

func (c *Client) AdminProviderRegister(ctx context.Context, providerID did.DID, endpoint string, proofs ucan.Container, options ...invocation.Option) (ucan.Receipt, error) {
	if c.issuer.DID() != c.uploadServiceID {
		return nil, fmt.Errorf("admin operation not permitted: issuer DID %s does not match upload service ID %s", c.issuer.DID(), c.uploadServiceID)
	}

	if proofs == nil {
		return nil, fmt.Errorf("missing proofs")
	}

	proofBytes, err := container.Encode(container.Raw, proofs)
	if err != nil {
		return nil, fmt.Errorf("encoding proofs: %w", err)
	}

	options = slices.Clone(options)
	options = append(
		options,
		invocation.WithAudience(c.uploadServiceID),
	)

	inv, err := providercap.Register.Invoke(
		c.issuer,
		c.uploadServiceID,
		&providercap.RegisterArguments{
			Provider: providerID,
			Endpoint: endpoint,
			Proofs:   proofBytes,
		},
		options...,
	)
	if err != nil {
		return nil, fmt.Errorf("invoking provider register: %w", err)
	}

	_, rcpt, _, err := ucan_client.Execute[*providercap.RegisterOK](ctx, c.client, c.logger, inv)
	if err != nil {
		return nil, fmt.Errorf("executing provider register invocation: %w", err)
	}
	return rcpt, nil
}

func (c *Client) AdminProviderDeregister(ctx context.Context, providerID did.DID, options ...invocation.Option) (ucan.Receipt, error) {
	if c.issuer.DID() != c.uploadServiceID {
		return nil, fmt.Errorf("admin operation not permitted: signer DID %s does not match upload service ID %s", c.issuer.DID(), c.uploadServiceID)
	}

	options = slices.Clone(options)
	options = append(
		options,
		invocation.WithAudience(c.uploadServiceID),
	)

	inv, err := providercap.Deregister.Invoke(
		c.issuer,
		c.uploadServiceID,
		&providercap.DeregisterArguments{
			Provider: providerID,
		},
		options...,
	)
	if err != nil {
		return nil, fmt.Errorf("invoking provider deregister: %w", err)
	}

	_, rcpt, _, err := ucan_client.Execute[*providercap.DeregisterOK](ctx, c.client, c.logger, inv)
	if err != nil {
		return nil, fmt.Errorf("executing provider deregister invocation: %w", err)
	}
	return rcpt, nil
}

func (c *Client) AdminProviderList(ctx context.Context, options ...invocation.Option) (*providercap.ListOK, ucan.Receipt, error) {
	if c.issuer.DID() != c.uploadServiceID {
		return nil, nil, fmt.Errorf("admin operation not permitted: signer DID %s does not match upload service ID %s", c.issuer.DID(), c.uploadServiceID)
	}

	options = slices.Clone(options)
	options = append(
		options,
		invocation.WithAudience(c.uploadServiceID),
	)

	inv, err := providercap.List.Invoke(
		c.issuer,
		c.uploadServiceID,
		&providercap.ListArguments{},
		options...,
	)
	if err != nil {
		return nil, nil, fmt.Errorf("invoking provider list: %w", err)
	}

	listOK, rcpt, _, err := ucan_client.Execute[*providercap.ListOK](ctx, c.client, c.logger, inv)
	if err != nil {
		return nil, nil, fmt.Errorf("executing provider list invocation: %w", err)
	}
	return listOK, rcpt, nil
}

func (c *Client) AdminProviderWeightSet(ctx context.Context, providerID did.DID, weight int, replicationWeight int, options ...invocation.Option) (ucan.Receipt, error) {
	if c.issuer.DID() != c.uploadServiceID {
		return nil, fmt.Errorf("admin operation not permitted: signer DID %s does not match upload service ID %s", c.issuer.DID(), c.uploadServiceID)
	}

	options = slices.Clone(options)
	options = append(
		options,
		invocation.WithAudience(c.uploadServiceID),
	)

	inv, err := weightcap.Set.Invoke(
		c.issuer,
		c.uploadServiceID,
		&weightcap.SetArguments{
			Provider:          providerID,
			Weight:            int64(weight),
			ReplicationWeight: int64(replicationWeight),
		},
		options...,
	)
	if err != nil {
		return nil, fmt.Errorf("invoking provider weight set: %w", err)
	}

	_, rcpt, _, err := ucan_client.Execute[*weightcap.SetOK](ctx, c.client, c.logger, inv)
	if err != nil {
		return nil, fmt.Errorf("executing provider weight set invocation: %w", err)
	}
	return rcpt, nil
}

// RoutingPut replaces the candidate set of the routing policy. The issuer must
// hold a proof chain from proofs rooted at the policy; proofs may be nil when
// the issuer is the policy itself.
func (c *Client) RoutingPut(ctx context.Context, policy did.DID, candidates []did.DID, proofs ucanlib.ProofStore, options ...invocation.Option) (ucan.Receipt, error) {
	entries := make(map[did.DID]routingcmds.Candidate, len(candidates))
	for _, cand := range candidates {
		entries[cand] = routingcmds.Candidate{}
	}
	args := &routingcmds.PutArguments{Candidates: routingcmds.CandidateSet{Entries: entries}}
	rcpt, err := invokeRouting(c, ctx, routingcmds.Put, policy, args, proofs, options...)
	if err != nil {
		return nil, fmt.Errorf("routing put: %w", err)
	}
	return rcpt, nil
}

// RoutingUse sets the routing policy referenced by the space, or clears it when
// policy is nil. The issuer must hold a proof chain from proofs rooted at the
// space; proofs may be nil when the issuer is the space itself.
func (c *Client) RoutingUse(ctx context.Context, space did.DID, policy *did.DID, proofs ucanlib.ProofStore, options ...invocation.Option) (ucan.Receipt, error) {
	args := &routingcmds.UseArguments{Policy: policy}
	rcpt, err := invokeRouting(c, ctx, routingcmds.Use, space, args, proofs, options...)
	if err != nil {
		return nil, fmt.Errorf("routing use: %w", err)
	}
	return rcpt, nil
}

// invokeRouting builds and executes a routing command invocation on subject,
// attaching the proof chain from proofs when one is supplied.
func invokeRouting[A, O binding.CBORValue](c *Client, ctx context.Context, cmd binding.Binding[A, O], subject did.DID, args A, proofs ucanlib.ProofStore, options ...invocation.Option) (ucan.Receipt, error) {
	options = slices.Clone(options)
	options = append(options, invocation.WithAudience(c.uploadServiceID))

	var dlgs []ucan.Delegation
	if proofs != nil {
		chain, links, err := proofs.ProofChain(ctx, c.issuer.DID(), cmd.Command, subject)
		if err != nil {
			return nil, fmt.Errorf("building proof chain: %w", err)
		}
		dlgs = chain
		options = append(options, invocation.WithProofs(links...))
	}

	inv, err := cmd.Invoke(c.issuer, subject, args, options...)
	if err != nil {
		return nil, fmt.Errorf("invoking %s: %w", cmd.Command, err)
	}

	_, rcpt, _, err := ucan_client.Execute[O](ctx, c.client, c.logger, inv, execution.WithDelegations(dlgs...))
	if err != nil {
		return nil, fmt.Errorf("executing %s invocation: %w", cmd.Command, err)
	}
	return rcpt, nil
}
