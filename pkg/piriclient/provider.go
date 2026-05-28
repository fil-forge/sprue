package piriclient

import (
	"net/url"

	"github.com/fil-forge/ucantone/did"
	"github.com/fil-forge/ucantone/ucan"
	"go.uber.org/zap"
)

// Provider creates piri clients for communicating with storage nodes.
type Provider interface {
	Client(id did.DID, endpoint url.URL) (*Client, error)
}

// PiriProvider is the default Provider that creates HTTP-connected piri clients.
type PiriProvider struct {
	signer ucan.Signer
	logger *zap.Logger
}

var _ Provider = (*PiriProvider)(nil)

func NewProvider(signer ucan.Signer, logger *zap.Logger) *PiriProvider {
	return &PiriProvider{signer: signer, logger: logger}
}

// Client provides a client configured to communicate with the specified storage
// node.
func (p *PiriProvider) Client(id did.DID, endpoint url.URL) (*Client, error) {
	return New(&endpoint, id, p.signer, p.logger)
}
