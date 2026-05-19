package client

import (
	"context"
	"fmt"
	"net/url"
	"slices"

	providercap "github.com/fil-forge/sprue/pkg/commands/admin/provider"
	weightcap "github.com/fil-forge/sprue/pkg/commands/admin/provider/weight"
	"github.com/fil-forge/sprue/pkg/lib/ucan_client"
	"github.com/fil-forge/ucantone/client"
	"github.com/fil-forge/ucantone/did"
	"github.com/fil-forge/ucantone/ucan"
	"github.com/fil-forge/ucantone/ucan/invocation"
	"go.uber.org/zap"
)

type Client struct {
	uploadServiceID did.DID
	client          *client.HTTPClient
	signer          ucan.Signer
	logger          *zap.Logger
}

func New(uploadServiceID did.DID, endpoint *url.URL, signer ucan.Signer, logger *zap.Logger) (*Client, error) {
	client, err := client.NewHTTP(endpoint)
	if err != nil {
		return nil, fmt.Errorf("creating HTTP client: %w", err)
	}
	return NewWithClient(uploadServiceID, client, signer, logger), nil
}

func NewWithClient(uploadServiceID did.DID, client *client.HTTPClient, signer ucan.Signer, logger *zap.Logger) *Client {
	return &Client{
		uploadServiceID: uploadServiceID,
		signer:          signer,
		client:          client,
		logger:          logger,
	}
}

func (c *Client) AdminProviderRegister(ctx context.Context, providerID did.DID, endpoint string, options ...invocation.Option) (ucan.Receipt, error) {
	if c.signer.DID() != c.uploadServiceID {
		return nil, fmt.Errorf("admin operation not permitted: signer DID %s does not match upload service ID %s", c.signer.DID(), c.uploadServiceID)
	}

	options = slices.Clone(options)
	options = append(
		options,
		invocation.WithAudience(c.uploadServiceID),
	)

	inv, err := providercap.Register.Invoke(
		c.signer,
		c.uploadServiceID,
		&providercap.RegisterArguments{
			Provider: providerID,
			Endpoint: endpoint,
		},
		options...,
	)
	if err != nil {
		return nil, fmt.Errorf("invoking provider register: %w", err)
	}

	_, rcpt, err := ucan_client.Execute[*providercap.RegisterOK](ctx, c.client, c.logger, inv)
	if err != nil {
		return nil, fmt.Errorf("executing provider register invocation: %w", err)
	}
	return rcpt, nil
}

func (c *Client) AdminProviderDeregister(ctx context.Context, providerID did.DID, options ...invocation.Option) (ucan.Receipt, error) {
	if c.signer.DID() != c.uploadServiceID {
		return nil, fmt.Errorf("admin operation not permitted: signer DID %s does not match upload service ID %s", c.signer.DID(), c.uploadServiceID)
	}

	options = slices.Clone(options)
	options = append(
		options,
		invocation.WithAudience(c.uploadServiceID),
	)

	inv, err := providercap.Deregister.Invoke(
		c.signer,
		c.uploadServiceID,
		&providercap.DeregisterArguments{
			Provider: providerID,
		},
		options...,
	)
	if err != nil {
		return nil, fmt.Errorf("invoking provider deregister: %w", err)
	}

	_, rcpt, err := ucan_client.Execute[*providercap.DeregisterOK](ctx, c.client, c.logger, inv)
	if err != nil {
		return nil, fmt.Errorf("executing provider deregister invocation: %w", err)
	}
	return rcpt, nil
}

func (c *Client) AdminProviderList(ctx context.Context, options ...invocation.Option) (*providercap.ListOK, ucan.Receipt, error) {
	if c.signer.DID() != c.uploadServiceID {
		return nil, nil, fmt.Errorf("admin operation not permitted: signer DID %s does not match upload service ID %s", c.signer.DID(), c.uploadServiceID)
	}

	options = slices.Clone(options)
	options = append(
		options,
		invocation.WithAudience(c.uploadServiceID),
	)

	inv, err := providercap.List.Invoke(
		c.signer,
		c.uploadServiceID,
		&providercap.ListArguments{},
		options...,
	)
	if err != nil {
		return nil, nil, fmt.Errorf("invoking provider list: %w", err)
	}

	listOK, rcpt, err := ucan_client.Execute[*providercap.ListOK](ctx, c.client, c.logger, inv)
	if err != nil {
		return nil, nil, fmt.Errorf("executing provider list invocation: %w", err)
	}
	return listOK, rcpt, nil
}

func (c *Client) AdminProviderWeightSet(ctx context.Context, providerID did.DID, weight int, replicationWeight int, options ...invocation.Option) (ucan.Receipt, error) {
	if c.signer.DID() != c.uploadServiceID {
		return nil, fmt.Errorf("admin operation not permitted: signer DID %s does not match upload service ID %s", c.signer.DID(), c.uploadServiceID)
	}

	options = slices.Clone(options)
	options = append(
		options,
		invocation.WithAudience(c.uploadServiceID),
	)

	inv, err := weightcap.Set.Invoke(
		c.signer,
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

	_, rcpt, err := ucan_client.Execute[*weightcap.SetOK](ctx, c.client, c.logger, inv)
	if err != nil {
		return nil, fmt.Errorf("executing provider weight set invocation: %w", err)
	}
	return rcpt, nil
}
