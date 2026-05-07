package client

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/fil-forge/go-libstoracha/capabilities/types"
	"github.com/fil-forge/go-ucanto/client"
	"github.com/fil-forge/go-ucanto/core/delegation"
	"github.com/fil-forge/go-ucanto/core/invocation"
	"github.com/fil-forge/go-ucanto/core/ipld"
	"github.com/fil-forge/go-ucanto/core/receipt"
	"github.com/fil-forge/go-ucanto/core/result"
	fdm "github.com/fil-forge/go-ucanto/core/result/failure/datamodel"
	"github.com/fil-forge/go-ucanto/did"
	ucan_http "github.com/fil-forge/go-ucanto/transport/http"
	"github.com/fil-forge/go-ucanto/ucan"
	"github.com/fil-forge/sprue/pkg/capabilities/admin/provider"
)

type Option func(*Client) error

// WithServiceURL configures the URL to use when sending invocations. Unused
// when [WithConnection] option is passed.
func WithServiceURL(serviceURL string) Option {
	return func(c *Client) error {
		parsedURL, err := url.Parse(serviceURL)
		if err != nil {
			return fmt.Errorf("parsing service URL: %w", err)
		}
		c.serviceURL = parsedURL
		return nil
	}
}

// WithHTTPClient configures the HTTP client to use when sending invocation
// requests. Unused when [WithConnection] option is passed.
func WithHTTPClient(httpClient *http.Client) Option {
	return func(c *Client) error {
		c.httpClient = httpClient
		return nil
	}
}

// WithConnection configures the client connection to use for invocations.
func WithConnection(conn client.Connection) Option {
	return func(c *Client) error {
		c.Connection = conn
		return nil
	}
}

type Client struct {
	serviceURL *url.URL
	httpClient *http.Client
	Connection client.Connection
}

func New(serviceID ucan.Principal, options ...Option) (*Client, error) {
	c := Client{
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}
	for _, opt := range options {
		if err := opt(&c); err != nil {
			return nil, err
		}
	}
	if c.Connection != nil {
		return &c, nil
	}
	if c.serviceURL == nil {
		if !strings.HasPrefix(serviceID.DID().String(), "did:web:") {
			return nil, fmt.Errorf("service URL must be provided if no connection is set")
		}
		// For did:web, we can derive the service URL from the DID by replacing
		// "did:web:" with "https://".
		domain := strings.TrimPrefix(serviceID.DID().String(), "did:web:")
		u, err := url.Parse(fmt.Sprintf("https://%s", domain))
		if err != nil {
			return nil, fmt.Errorf("parsing derived service URL: %w", err)
		}
		c.serviceURL = u
	}
	channel := ucan_http.NewChannel(c.serviceURL, ucan_http.WithClient(c.httpClient))
	conn, err := client.NewConnection(serviceID, channel)
	if err != nil {
		return nil, fmt.Errorf("creating connection: %w", err)
	}
	c.Connection = conn
	return &c, nil
}

func (c *Client) AdminProviderRegister(ctx context.Context, signer ucan.Signer, endpoint string, proof delegation.Delegation, options ...delegation.Option) (provider.RegisterOk, error) {
	inv, err := provider.Register.Invoke(
		signer,
		c.Connection.ID(),
		c.Connection.ID().DID().String(),
		provider.RegisterCaveats{
			Endpoint: endpoint,
			Proof:    proof.Link(),
		},
		options...,
	)
	if err != nil {
		return provider.RegisterOk{}, fmt.Errorf("invoking provider register: %w", err)
	}
	for b, err := range proof.Blocks() {
		if err != nil {
			return provider.RegisterOk{}, fmt.Errorf("iterating proof blocks: %w", err)
		}
		if err := inv.Attach(b); err != nil {
			return provider.RegisterOk{}, fmt.Errorf("attaching proof block: %w", err)
		}
	}

	res, err := client.Execute(ctx, []invocation.Invocation{inv}, c.Connection)
	if err != nil {
		return provider.RegisterOk{}, fmt.Errorf("executing invocation: %w", err)
	}

	rcptLink, ok := res.Get(inv.Link())
	if !ok {
		return provider.RegisterOk{}, fmt.Errorf("no receipt found for invocation in response")
	}

	reader := receipt.NewAnyReceiptReader(types.Converters...)
	rcpt, err := reader.Read(rcptLink, res.Blocks())
	if err != nil {
		return provider.RegisterOk{}, fmt.Errorf("reading receipt: %w", err)
	}

	return result.MatchResultR2(
		rcpt.Out(),
		func(o ipld.Node) (provider.RegisterOk, error) {
			v, err := ipld.Rebind[provider.RegisterOk](o, provider.RegisterOkType(), types.Converters...)
			if err != nil {
				return provider.RegisterOk{}, fmt.Errorf("binding register success: %w", err)
			}
			return v, nil
		},
		func(x ipld.Node) (provider.RegisterOk, error) {
			return provider.RegisterOk{}, fdm.Bind(x)
		},
	)
}

func (c *Client) AdminProviderDeregister(ctx context.Context, signer ucan.Signer, providerID did.DID, options ...delegation.Option) (provider.DeregisterOk, error) {
	inv, err := provider.Deregister.Invoke(
		signer,
		c.Connection.ID(),
		c.Connection.ID().DID().String(),
		provider.DeregisterCaveats{
			Provider: providerID,
		},
		options...,
	)
	if err != nil {
		return provider.DeregisterOk{}, fmt.Errorf("invoking provider deregister: %w", err)
	}

	res, err := client.Execute(ctx, []invocation.Invocation{inv}, c.Connection)
	if err != nil {
		return provider.DeregisterOk{}, fmt.Errorf("executing invocation: %w", err)
	}

	rcptLink, ok := res.Get(inv.Link())
	if !ok {
		return provider.DeregisterOk{}, fmt.Errorf("no receipt found for invocation in response")
	}

	reader := receipt.NewAnyReceiptReader(types.Converters...)
	rcpt, err := reader.Read(rcptLink, res.Blocks())
	if err != nil {
		return provider.DeregisterOk{}, fmt.Errorf("reading receipt: %w", err)
	}

	return result.MatchResultR2(
		rcpt.Out(),
		func(o ipld.Node) (provider.DeregisterOk, error) {
			v, err := ipld.Rebind[provider.DeregisterOk](o, provider.DeregisterOkType(), types.Converters...)
			if err != nil {
				return provider.DeregisterOk{}, fmt.Errorf("binding deregister success: %w", err)
			}
			return v, nil
		},
		func(x ipld.Node) (provider.DeregisterOk, error) {
			return provider.DeregisterOk{}, fdm.Bind(x)
		},
	)
}

func (c *Client) AdminProviderList(ctx context.Context, signer ucan.Signer, options ...delegation.Option) (provider.ListOk, error) {
	inv, err := provider.List.Invoke(
		signer,
		c.Connection.ID(),
		c.Connection.ID().DID().String(),
		provider.ListCaveats{},
		options...,
	)
	if err != nil {
		return provider.ListOk{}, fmt.Errorf("invoking provider list: %w", err)
	}

	res, err := client.Execute(ctx, []invocation.Invocation{inv}, c.Connection)
	if err != nil {
		return provider.ListOk{}, fmt.Errorf("executing invocation: %w", err)
	}

	rcptLink, ok := res.Get(inv.Link())
	if !ok {
		return provider.ListOk{}, fmt.Errorf("no receipt found for invocation in response")
	}

	reader := receipt.NewAnyReceiptReader(types.Converters...)
	rcpt, err := reader.Read(rcptLink, res.Blocks())
	if err != nil {
		return provider.ListOk{}, fmt.Errorf("reading receipt: %w", err)
	}

	return result.MatchResultR2(
		rcpt.Out(),
		func(o ipld.Node) (provider.ListOk, error) {
			v, err := ipld.Rebind[provider.ListOk](o, provider.ListOkType(), types.Converters...)
			if err != nil {
				return provider.ListOk{}, fmt.Errorf("binding list success: %w", err)
			}
			return v, nil
		},
		func(x ipld.Node) (provider.ListOk, error) {
			return provider.ListOk{}, fdm.Bind(x)
		},
	)
}

func (c *Client) AdminProviderWeightSet(ctx context.Context, signer ucan.Signer, providerID did.DID, weight int, replicationWeight int, options ...delegation.Option) (provider.WeightSetOk, error) {
	inv, err := provider.WeightSet.Invoke(
		signer,
		c.Connection.ID(),
		c.Connection.ID().DID().String(),
		provider.WeightSetCaveats{
			Provider:          providerID,
			Weight:            weight,
			ReplicationWeight: replicationWeight,
		},
		options...,
	)
	if err != nil {
		return provider.WeightSetOk{}, fmt.Errorf("invoking provider weight set: %w", err)
	}

	res, err := client.Execute(ctx, []invocation.Invocation{inv}, c.Connection)
	if err != nil {
		return provider.WeightSetOk{}, fmt.Errorf("executing invocation: %w", err)
	}

	rcptLink, ok := res.Get(inv.Link())
	if !ok {
		return provider.WeightSetOk{}, fmt.Errorf("no receipt found for invocation in response")
	}

	reader := receipt.NewAnyReceiptReader(types.Converters...)
	rcpt, err := reader.Read(rcptLink, res.Blocks())
	if err != nil {
		return provider.WeightSetOk{}, fmt.Errorf("reading receipt: %w", err)
	}

	return result.MatchResultR2(
		rcpt.Out(),
		func(o ipld.Node) (provider.WeightSetOk, error) {
			v, err := ipld.Rebind[provider.WeightSetOk](o, provider.WeightSetOkType(), types.Converters...)
			if err != nil {
				return provider.WeightSetOk{}, fmt.Errorf("binding weight set success: %w", err)
			}
			return v, nil
		},
		func(x ipld.Node) (provider.WeightSetOk, error) {
			return provider.WeightSetOk{}, fdm.Bind(x)
		},
	)
}
