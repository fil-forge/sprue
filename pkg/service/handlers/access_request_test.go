package handlers

import (
	"bytes"
	"context"
	"errors"
	"net/url"
	"testing"

	"github.com/fil-forge/libforge/commands/access"
	"github.com/fil-forge/libforge/didmailto"
	"github.com/fil-forge/sprue/internal/config"
	"github.com/fil-forge/sprue/internal/testutil"
	"github.com/fil-forge/sprue/pkg/identity"
	edm "github.com/fil-forge/ucantone/errors/datamodel"
	"github.com/fil-forge/ucantone/execution"
	"github.com/fil-forge/ucantone/ucan/invocation"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

type mockMailer struct {
	lastTo  string
	lastURL url.URL
	err     error
}

func (m *mockMailer) SendValidation(ctx context.Context, to string, validationURL url.URL) error {
	m.lastTo = to
	m.lastURL = validationURL
	return m.err
}

func newTestIdentity(t *testing.T) *identity.Identity {
	t.Helper()
	id, err := identity.New("")
	require.NoError(t, err)
	return id
}

func TestAccessRequestHandler(t *testing.T) {
	logger := zaptest.NewLogger(t)
	serverCfg := config.ServerConfig{
		Host:      "localhost",
		Port:      8080,
		PublicURL: "http://localhost:8080",
	}

	t.Run("success", func(t *testing.T) {
		id := newTestIdentity(t)
		m := &mockMailer{}
		handler := NewAccessRequestHandler(serverCfg, id, m, logger)

		account, err := didmailto.New("alice@example.com")
		require.NoError(t, err)

		args := access.RequestArguments{
			Issuer: account,
			Attenuations: []access.CapabilityRequest{
				{Command: "/"},
			},
		}

		agent := testutil.RandomSigner(t)

		inv, err := access.Request.Invoke(
			agent,
			id.Signer.DID(),
			&args,
			invocation.WithAudience(id.Signer.DID()),
		)
		require.NoError(t, err)

		req := execution.NewRequest(t.Context(), inv)
		res, err := execution.NewResponse(req.Invocation().Task().Link(), execution.WithSigner(id.Signer))
		require.NoError(t, err)

		err = handler.Handler(req, res)
		require.NoError(t, err)

		o, x := res.Receipt().Out().Unpack()
		require.Nil(t, x)
		require.NotNil(t, o)

		var ok access.RequestOK
		require.NoError(t, ok.UnmarshalCBOR(bytes.NewReader(o)))
		require.Equal(t, inv.Link(), ok.Request)
		require.NotZero(t, ok.Expiration)

		require.Equal(t, "alice@example.com", m.lastTo)
		require.Contains(t, m.lastURL.String(), "/validate-email")
		require.Contains(t, m.lastURL.Query().Get("mode"), "authorize")
	})

	t.Run("invalid account DID", func(t *testing.T) {
		id := newTestIdentity(t)
		m := &mockMailer{}
		handler := NewAccessRequestHandler(serverCfg, id, m, logger)

		// A did:key (not did:mailto) — didmailto.Parse will reject it.
		nonMailtoSigner := testutil.RandomSigner(t)
		args := access.RequestArguments{
			Issuer: nonMailtoSigner.DID(),
			Attenuations: []access.CapabilityRequest{
				{Command: "/"},
			},
		}

		agent := testutil.RandomSigner(t)

		inv, err := access.Request.Invoke(
			agent,
			id.Signer.DID(),
			&args,
			invocation.WithAudience(id.Signer.DID()),
		)
		require.NoError(t, err)

		req := execution.NewRequest(t.Context(), inv)
		res, err := execution.NewResponse(req.Invocation().Task().Link(), execution.WithSigner(id.Signer))
		require.NoError(t, err)

		err = handler.Handler(req, res)
		require.NoError(t, err)

		_, x := res.Receipt().Out().Unpack()
		require.NotNil(t, x)

		var model edm.ErrorModel
		require.NoError(t, model.UnmarshalCBOR(bytes.NewReader(x)))
		require.Equal(t, access.InvalidAuthorizationAccountErrorName, model.Name())
	})

	t.Run("mailer error", func(t *testing.T) {
		id := newTestIdentity(t)
		m := &mockMailer{err: errors.New("smtp failure")}
		handler := NewAccessRequestHandler(serverCfg, id, m, logger)

		account, err := didmailto.New("alice@example.com")
		require.NoError(t, err)

		args := access.RequestArguments{
			Issuer: account,
			Attenuations: []access.CapabilityRequest{
				{Command: "/"},
			},
		}

		agent := testutil.RandomSigner(t)

		inv, err := access.Request.Invoke(
			agent,
			id.Signer.DID(),
			&args,
			invocation.WithAudience(id.Signer.DID()),
		)
		require.NoError(t, err)

		req := execution.NewRequest(t.Context(), inv)
		res, err := execution.NewResponse(req.Invocation().Task().Link(), execution.WithSigner(id.Signer))
		require.NoError(t, err)

		err = handler.Handler(req, res)
		require.Error(t, err)
		require.Contains(t, err.Error(), "sending validation email")
	})

	t.Run("public URL fallback", func(t *testing.T) {
		id := newTestIdentity(t)
		m := &mockMailer{}
		cfgNoPublicURL := config.ServerConfig{
			Host: "myhost",
			Port: 9090,
		}
		handler := NewAccessRequestHandler(cfgNoPublicURL, id, m, logger)

		account, err := didmailto.New("bob@example.com")
		require.NoError(t, err)

		args := access.RequestArguments{
			Issuer: account,
			Attenuations: []access.CapabilityRequest{
				{Command: "/"},
			},
		}

		agent := testutil.RandomSigner(t)

		inv, err := access.Request.Invoke(
			agent,
			id.Signer.DID(),
			&args,
			invocation.WithAudience(id.Signer.DID()),
		)
		require.NoError(t, err)

		req := execution.NewRequest(t.Context(), inv)
		res, err := execution.NewResponse(req.Invocation().Task().Link(), execution.WithSigner(id.Signer))
		require.NoError(t, err)

		err = handler.Handler(req, res)
		require.NoError(t, err)

		require.Contains(t, m.lastURL.String(), "http://myhost:9090/validate-email")
	})
}
