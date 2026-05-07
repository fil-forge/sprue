package handlers

import (
	"context"
	"fmt"
	"net/url"
	"time"

	"go.uber.org/zap"

	"github.com/fil-forge/go-libstoracha/capabilities/access"
	"github.com/fil-forge/go-ucanto/core/delegation"
	"github.com/fil-forge/go-ucanto/core/invocation"
	"github.com/fil-forge/go-ucanto/core/receipt/fx"
	"github.com/fil-forge/go-ucanto/core/result"
	"github.com/fil-forge/go-ucanto/core/result/failure"
	"github.com/fil-forge/go-ucanto/did"
	"github.com/fil-forge/go-ucanto/server"
	"github.com/fil-forge/go-ucanto/ucan"
	"github.com/fil-forge/sprue/internal/config"
	"github.com/fil-forge/sprue/pkg/identity"
	"github.com/fil-forge/sprue/pkg/lib/didmailto"
	"github.com/fil-forge/sprue/pkg/lib/errors"
	"github.com/fil-forge/sprue/pkg/lib/ucans"
	"github.com/fil-forge/sprue/pkg/mailer"
	"github.com/ipld/go-ipld-prime/datamodel"
)

const (
	InvalidAuthorizationAccountErrorName  = "InvalidAuthorizationAccount"
	InvalidAuthorizationAudienceErrorName = "InvalidAuthorizationAudience"
)

// Standard email flow - create confirmation delegation and send email
// We allow granting access within the next 15 minutes
const confirmationTTL = time.Minute * 15

var (
	ErrMissingAuthorizationAccount  = errors.New(InvalidAuthorizationAccountErrorName, "missing authorization account DID")
	ErrInvalidAuthorizationAccount  = errors.New(InvalidAuthorizationAccountErrorName, "invalid authorization account DID")
	ErrInvalidAuthorizationAudience = errors.New(InvalidAuthorizationAudienceErrorName, "invalid authorization audience DID")
)

// WithAccessAuthorizeMethod registers the access/authorize handler.
func WithAccessAuthorizeMethod(serverCfg config.ServerConfig, id *identity.Identity, mailer mailer.Mailer, logger *zap.Logger) server.Option {
	return server.WithServiceMethod(
		access.AuthorizeAbility,
		server.Provide(
			access.Authorize,
			AccessAuthorizeHandler(serverCfg, id, mailer, logger),
		),
	)
}

func AccessAuthorizeHandler(serverCfg config.ServerConfig, id *identity.Identity, mailer mailer.Mailer, logger *zap.Logger) server.HandlerFunc[access.AuthorizeCaveats, access.AuthorizeOk, failure.IPLDBuilderFailure] {
	log := logger.With(zap.String("handler", access.AuthorizeAbility))
	return func(ctx context.Context,
		cap ucan.Capability[access.AuthorizeCaveats],
		inv invocation.Invocation,
		iCtx server.InvocationContext,
	) (result.Result[access.AuthorizeOk, failure.IPLDBuilderFailure], fx.Effects, error) {
		// Get the account DID from the caveats
		if cap.Nb().Iss == nil {
			log.Warn("missing account")
			return result.Error[access.AuthorizeOk, failure.IPLDBuilderFailure](ErrMissingAuthorizationAccount), nil, nil
		}
		account, err := didmailto.Parse(*cap.Nb().Iss)
		if err != nil {
			log.Warn("invalid account", zap.String("account", *cap.Nb().Iss))
			return result.Error[access.AuthorizeOk, failure.IPLDBuilderFailure](
				errors.New(InvalidAuthorizationAccountErrorName, "invalid authorization account DID: %v", err),
			), nil, nil
		}
		// we should be able to extract the email from the DID since we just
		// parsed it as a did:mailto:
		email, err := didmailto.Email(account)
		if err != nil {
			return nil, nil, fmt.Errorf("extracting email from mailto DID: %w", err)
		}
		audience, err := did.Parse(cap.With())
		if err != nil {
			log.Warn("invalid audience", zap.String("audience", cap.With()))
			return result.Error[access.AuthorizeOk, failure.IPLDBuilderFailure](
				errors.New(InvalidAuthorizationAudienceErrorName, "invalid authorization audience DID: %v", err),
			), nil, nil
		}

		agent := inv.Issuer().DID()
		log := log.With(
			zap.Stringer("agent", agent),
			zap.Stringer("account", account),
			zap.Stringer("audience", audience),
		)
		log.Debug("authorizing access")

		exp := int(time.Now().Add(confirmationTTL).Unix())

		// We issue `access/confirm` invocation which will
		// get embedded in the URL that we send to the user. When user clicks the
		// link we'll get this delegation back in the `/validate-email` endpoint
		// which will allow us to verify that it was the user who clicked the link
		// and not some attacker impersonating the user. We will know that because
		// the `with` field is our service DID and only private key holder is able
		// to issue such delegation.
		//
		// We limit lifetime of this UCAN to 15 minutes to reduce the attack
		// surface where an attacker could attempt concurrent authorization
		// request in attempt confuse a user into clicking the wrong link.
		confirmation, err := access.Confirm.Invoke(
			id.Signer,
			// audience same as issuer because this is a service invocation
			// that will get handled by access/confirm handler
			// but only if the receiver of this email wants it to be
			id.Signer.DID(),
			id.DID(),
			// We link to the authorization request so that this attestation can
			// not be used to authorize a different request.
			access.ConfirmCaveats{
				// we copy request details and set the `aud` field to the agent DID
				// that requested the authorization.
				Iss: account,
				Aud: audience,
				Att: cap.Nb().Att,
				// Link to the invocation that requested the authorization.
				Cause: inv.Link(),
			},
			delegation.WithExpiration(exp),
			// we copy the facts in so that information can be passed
			// from the invoker of this capability to the invoker of the confirm
			// capability - we use this, for example, to let bsky.storage users
			// specify that they should be redirected back to bsky.storage after
			// completing the Stripe plan selection flow
			delegation.WithFacts(toFactBuilders(inv.Facts())),
		)
		if err != nil {
			log.Error("failed to create confirmation delegation", zap.Error(err))
			return nil, nil, fmt.Errorf("creating confirmation delegation: %w", err)
		}

		confirmationStr, err := ucans.FormatDelegations(confirmation)
		if err != nil {
			log.Error("failed to format confirmation", zap.Error(err))
			return nil, nil, fmt.Errorf("formatting confirmation: %w", err)
		}

		pubUrlStr := serverCfg.PublicURL
		if pubUrlStr == "" {
			pubUrlStr = fmt.Sprintf("http://%s:%d", serverCfg.Host, serverCfg.Port)
		}
		validationURL, err := url.Parse(fmt.Sprintf("%s/validate-email?ucan=%s&mode=authorize", pubUrlStr, confirmationStr))
		if err != nil {
			log.Error("failed to parse validation URL", zap.Error(err))
			return nil, nil, fmt.Errorf("parsing validation URL: %w", err)
		}

		err = mailer.SendValidation(ctx, email, *validationURL)
		if err != nil {
			log.Error("failed to send validation email", zap.Error(err))
			return nil, nil, fmt.Errorf("sending validation email: %w", err)
		}

		ok := result.Ok[access.AuthorizeOk, failure.IPLDBuilderFailure](access.AuthorizeOk{
			// link to this authorization request
			Request: inv.Link(),
			// let client know when the confirmation will expire
			Expiration: exp,
		})

		// link to the authorization confirmation so it could be used to lookup
		// the delegation by the authorization request.
		join := fx.NewEffects(fx.WithJoin(fx.FromLink(confirmation.Root().Link())))

		return ok, join, nil
	}
}

type anyFactBuilder struct {
	fct ucan.Fact
}

func (afb anyFactBuilder) ToIPLD() (map[string]datamodel.Node, error) {
	nodeFacts := make(map[string]datamodel.Node, len(afb.fct))
	for k, v := range afb.fct {
		if node, ok := v.(datamodel.Node); ok {
			nodeFacts[k] = node
		} else {
			return nil, fmt.Errorf("fact %q is not an IPLD node", k)
		}
	}
	return nodeFacts, nil
}

func toFactBuilders(fcts []ucan.Fact) []ucan.FactBuilder {
	builders := make([]ucan.FactBuilder, len(fcts))
	for i, fct := range fcts {
		builders[i] = anyFactBuilder{fct: fct}
	}
	return builders
}
