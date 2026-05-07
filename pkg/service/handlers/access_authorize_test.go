package handlers

import (
	"context"
	"errors"
	"net/url"
	"testing"

	"github.com/fil-forge/go-libstoracha/capabilities/access"
	"github.com/fil-forge/go-ucanto/core/invocation"
	"github.com/fil-forge/go-ucanto/core/result"
	"github.com/fil-forge/go-ucanto/ucan"
	"github.com/fil-forge/sprue/internal/config"
	"github.com/fil-forge/sprue/pkg/identity"
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

func TestAccessAuthorizeHandler(t *testing.T) {
	logger := zaptest.NewLogger(t)
	serverCfg := config.ServerConfig{
		Host:      "localhost",
		Port:      8080,
		PublicURL: "http://localhost:8080",
	}

	t.Run("success", func(t *testing.T) {
		id := newTestIdentity(t)
		m := &mockMailer{}
		handler := AccessAuthorizeHandler(serverCfg, id, m, logger)

		iss := "did:mailto:example.com:alice"
		cap := ucan.NewCapability(
			access.AuthorizeAbility,
			id.DID(),
			access.AuthorizeCaveats{
				Iss: &iss,
				Att: []access.CapabilityRequest{{Can: "*"}},
			},
		)

		agent, err := identity.New("")
		require.NoError(t, err)

		inv, err := invocation.Invoke(agent.Signer, id.Signer, cap)
		require.NoError(t, err)

		res, effects, err := handler(context.Background(), cap, inv, nil)
		require.NoError(t, err)
		require.NotNil(t, effects)

		ok, fail := result.Unwrap(res)
		require.Nil(t, fail)
		require.Equal(t, inv.Link(), ok.Request)
		require.NotZero(t, ok.Expiration)

		require.Equal(t, "alice@example.com", m.lastTo)
		require.Contains(t, m.lastURL.String(), "/validate-email")
		require.Contains(t, m.lastURL.Query().Get("mode"), "authorize")
	})

	t.Run("missing account", func(t *testing.T) {
		id := newTestIdentity(t)
		m := &mockMailer{}
		handler := AccessAuthorizeHandler(serverCfg, id, m, logger)

		cap := ucan.NewCapability(
			access.AuthorizeAbility,
			id.DID(),
			access.AuthorizeCaveats{
				Iss: nil,
				Att: []access.CapabilityRequest{{Can: "*"}},
			},
		)

		agent, err := identity.New("")
		require.NoError(t, err)

		inv, err := invocation.Invoke(agent.Signer, id.Signer, cap)
		require.NoError(t, err)

		res, _, err := handler(context.Background(), cap, inv, nil)
		require.NoError(t, err)

		_, fail := result.Unwrap(res)
		require.NotNil(t, fail)
	})

	t.Run("invalid account DID", func(t *testing.T) {
		id := newTestIdentity(t)
		m := &mockMailer{}
		handler := AccessAuthorizeHandler(serverCfg, id, m, logger)

		iss := "did:key:z6MkhaXgBZDvotDkL5257faiztiGiC2QtKLGpbnnEGta2doK"
		cap := ucan.NewCapability(
			access.AuthorizeAbility,
			id.DID(),
			access.AuthorizeCaveats{
				Iss: &iss,
				Att: []access.CapabilityRequest{{Can: "*"}},
			},
		)

		agent, err := identity.New("")
		require.NoError(t, err)

		inv, err := invocation.Invoke(agent.Signer, id.Signer, cap)
		require.NoError(t, err)

		res, _, err := handler(context.Background(), cap, inv, nil)
		require.NoError(t, err)

		_, fail := result.Unwrap(res)
		require.NotNil(t, fail)
	})

	t.Run("invalid audience DID", func(t *testing.T) {
		id := newTestIdentity(t)
		m := &mockMailer{}
		handler := AccessAuthorizeHandler(serverCfg, id, m, logger)

		iss := "did:mailto:example.com:alice"
		cap := ucan.NewCapability(
			access.AuthorizeAbility,
			"not-a-did",
			access.AuthorizeCaveats{
				Iss: &iss,
				Att: []access.CapabilityRequest{{Can: "*"}},
			},
		)

		agent, err := identity.New("")
		require.NoError(t, err)

		inv, err := invocation.Invoke(agent.Signer, id.Signer, cap)
		require.NoError(t, err)

		res, _, err := handler(context.Background(), cap, inv, nil)
		require.NoError(t, err)

		_, fail := result.Unwrap(res)
		require.NotNil(t, fail)
	})

	t.Run("mailer error", func(t *testing.T) {
		id := newTestIdentity(t)
		m := &mockMailer{err: errors.New("smtp failure")}
		handler := AccessAuthorizeHandler(serverCfg, id, m, logger)

		iss := "did:mailto:example.com:alice"
		cap := ucan.NewCapability(
			access.AuthorizeAbility,
			id.DID(),
			access.AuthorizeCaveats{
				Iss: &iss,
				Att: []access.CapabilityRequest{{Can: "*"}},
			},
		)

		agent, err := identity.New("")
		require.NoError(t, err)

		inv, err := invocation.Invoke(agent.Signer, id.Signer, cap)
		require.NoError(t, err)

		_, _, err = handler(context.Background(), cap, inv, nil)
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
		handler := AccessAuthorizeHandler(cfgNoPublicURL, id, m, logger)

		iss := "did:mailto:example.com:bob"
		cap := ucan.NewCapability(
			access.AuthorizeAbility,
			id.DID(),
			access.AuthorizeCaveats{
				Iss: &iss,
				Att: []access.CapabilityRequest{{Can: "*"}},
			},
		)

		agent, err := identity.New("")
		require.NoError(t, err)

		inv, err := invocation.Invoke(agent.Signer, id.Signer, cap)
		require.NoError(t, err)

		_, _, err = handler(context.Background(), cap, inv, nil)
		require.NoError(t, err)

		require.Contains(t, m.lastURL.String(), "http://myhost:9090/validate-email")
	})
}
