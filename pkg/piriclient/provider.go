package piriclient

import (
	"net/http"
	"net/url"
	"time"

	"github.com/fil-forge/ucantone/client"
	"github.com/fil-forge/ucantone/did"
	"github.com/fil-forge/ucantone/ucan"
	"go.uber.org/zap"
)

// Connection settings for the shared outbound transport. http.DefaultClient's
// transport keeps 2 idle connections per host and no request timeout, which
// starves a handler that talks to one node repeatedly and lets a node that
// never answers hold a request open forever.
const (
	maxIdleConnsPerPiri = 64
	// Generous: a batched accept makes the node do per-blob work (location
	// claim, IPNI advertisement, PDP enqueue) before it answers.
	piriRequestTimeout = 30 * time.Minute
)

// Provider creates piri clients for communicating with storage nodes.
type Provider interface {
	Client(id did.DID, endpoint url.URL) (*Client, error)
}

// PiriProvider is the default Provider that creates HTTP-connected piri clients.
type PiriProvider struct {
	issuer ucan.Issuer
	logger *zap.Logger
	// http is shared by every client this provider hands out, so the
	// connection pool survives the per-request clients callers construct.
	http *http.Client
}

var _ Provider = (*PiriProvider)(nil)

func NewProvider(issuer ucan.Issuer, logger *zap.Logger) *PiriProvider {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.MaxIdleConnsPerHost = maxIdleConnsPerPiri
	transport.MaxIdleConns = maxIdleConnsPerPiri * 8
	return &PiriProvider{
		issuer: issuer,
		logger: logger,
		http:   &http.Client{Transport: transport, Timeout: piriRequestTimeout},
	}
}

// Client provides a client configured to communicate with the specified storage
// node.
func (p *PiriProvider) Client(id did.DID, endpoint url.URL) (*Client, error) {
	return New(&endpoint, id, p.issuer, p.logger, client.WithHTTPClient(p.http))
}
