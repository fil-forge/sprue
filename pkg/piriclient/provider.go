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
	// A backstop for a node that stops answering, not the expected duration.
	// The node executes a batch one accept at a time, doing per-blob work for
	// each (location claim, IPNI advertisement, PDP enqueue), so the figure is
	// sized against maxAcceptBatch: a full batch runs ~10s against a local
	// stack and ~20-30s once the indexer round trip is a real one, and this
	// leaves roughly double that before giving up.
	piriRequestTimeout = 60 * time.Second
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
	return &PiriProvider{
		issuer: issuer,
		logger: logger,
		http:   &http.Client{Transport: pooledTransport(), Timeout: piriRequestTimeout},
	}
}

// pooledTransport returns the default transport with the connection pool
// widened. A process that replaced http.DefaultTransport with some other
// RoundTripper keeps it as-is: the pool settings are a tuning detail, and
// honouring the caller's transport matters more than applying them.
func pooledTransport() http.RoundTripper {
	def, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return http.DefaultTransport
	}
	transport := def.Clone()
	transport.MaxIdleConnsPerHost = maxIdleConnsPerPiri
	transport.MaxIdleConns = maxIdleConnsPerPiri * 8
	return transport
}

// Client provides a client configured to communicate with the specified storage
// node.
func (p *PiriProvider) Client(id did.DID, endpoint url.URL) (*Client, error) {
	return New(&endpoint, id, p.issuer, p.logger, client.WithHTTPClient(p.http))
}
