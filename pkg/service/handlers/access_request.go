package handlers

import (
	"bytes"
	"fmt"
	"net/url"
	"time"

	"go.uber.org/zap"

	"github.com/fil-forge/libforge/attestation/didmailto"
	"github.com/fil-forge/libforge/commands/access"
	"github.com/fil-forge/libforge/identity"
	"github.com/fil-forge/sprue/internal/config"

	"github.com/fil-forge/sprue/pkg/mailer"
	"github.com/fil-forge/ucantone/binding"
	"github.com/fil-forge/ucantone/errors"
	"github.com/fil-forge/ucantone/ipld/datamodel"
	"github.com/fil-forge/ucantone/server"
	"github.com/fil-forge/ucantone/ucan"
	"github.com/fil-forge/ucantone/ucan/container"
	"github.com/fil-forge/ucantone/ucan/invocation"
	"github.com/fil-forge/ucantone/ucan/promise"
)

// Standard email flow - create confirmation delegation and send email
// We allow granting access within the next 15 minutes
const confirmationTTL = time.Minute * 15

var (
	ErrMissingAuthorizationAccount  = errors.New(access.InvalidAuthorizationAccountErrorName, "missing authorization account DID")
	ErrInvalidAuthorizationAccount  = errors.New(access.InvalidAuthorizationAccountErrorName, "invalid authorization account DID")
	ErrInvalidAuthorizationAudience = errors.New(access.InvalidAuthorizationAudienceErrorName, "invalid authorization audience DID")
)

func NewAccessRequestHandler(serverCfg config.ServerConfig, id identity.Identity, mailer mailer.Mailer, logger *zap.Logger) server.Route {
	log := logger.With(zap.Stringer("handler", access.Request))
	return access.Request.Route(
		func(req *binding.Request[*access.RequestArguments], res *binding.Response[*access.RequestOK]) error {
			args := req.Task().Arguments()
			account, err := didmailto.Parse(args.Issuer.String())
			if err != nil {
				log.Warn("failed to parse mailto DID", zap.Stringer("account", args.Issuer))
				return res.SetFailure(errors.New(access.InvalidAuthorizationAccountErrorName, "invalid authorization account DID: %v", err))
			}
			// we should be able to extract the email from the DID since we just
			// parsed it as a did:mailto:
			email, err := didmailto.Email(account)
			if err != nil {
				log.Warn("failed to extract email from DID", zap.Stringer("account", args.Issuer))
				return res.SetFailure(errors.New(access.InvalidAuthorizationAccountErrorName, "invalid authorization account DID: %v", err))
			}
			audience := req.Invocation().Subject()
			agent := req.Invocation().Issuer()
			log := log.With(
				zap.Stringer("agent", agent),
				zap.Stringer("account", account),
				zap.Stringer("audience", audience),
			)
			log.Debug("requesting access")

			exp := int(time.Now().Add(confirmationTTL).Unix())

			metaBytes := req.Invocation().MetadataBytes()
			meta := datamodel.Map{}
			if len(metaBytes) > 0 {
				if err := meta.UnmarshalCBOR(bytes.NewReader(metaBytes)); err != nil {
					log.Error("failed to unmarshal invocation metadata", zap.Error(err))
					return fmt.Errorf("unmarshaling invocation metadata: %w", err)
				}
			}

			// We issue an `/access/confirm` invocation which will be embedded in the
			// URL that we send to the user. When the user clicks the link we'll get
			// this invocation back in the `/validate-email` endpoint which will allow
			// us to verify that it was the user who clicked the link and not some
			// attacker impersonating the user. We will know that because the `subject`
			// will be our service DID and only private key holder is able to issue
			// such an invocation.
			//
			// We limit the lifetime of this UCAN to 15 minutes to reduce the attack
			// surface where an attacker could attempt concurrent authorization
			// requests in an attempt to confuse a user into clicking the wrong link.
			confirmation, err := access.Confirm.Invoke(
				id.Issuer,
				id.Issuer.DID(),
				// We link to the authorization request so that this invocation can
				// not be used to authorize a different request.
				&access.ConfirmArguments{
					// we copy request details and set the `aud` field to the agent DID
					// that requested the authorization.
					Issuer:       account,
					Audience:     audience,
					Attenuations: args.Attenuations,
					// Link to the task that requested the authorization.
					Cause: req.Invocation().Task().Link(),
				},
				// audience same as issuer because this is a service invocation
				// that will get handled by /access/confirm handler
				// but only if the receiver of this email wants it to be
				invocation.WithAudience(id.Issuer.DID()),
				invocation.WithExpiration(ucan.UnixTimestamp(exp)),
				// we copy the facts in so that information can be passed
				// from the invoker of this capability to the invoker of the confirm
				// capability - we use this, for example, to let bsky.storage users
				// specify that they should be redirected back to bsky.storage after
				// completing the Stripe plan selection flow
				invocation.WithMetadata(meta),
			)
			if err != nil {
				log.Error("failed to create confirmation delegation", zap.Error(err))
				return fmt.Errorf("creating confirmation delegation: %w", err)
			}

			confirmationStr, err := container.Encode(
				container.Base64urlGzip,
				container.New(container.WithInvocations(confirmation)),
			)
			if err != nil {
				log.Error("failed to format confirmation", zap.Error(err))
				return fmt.Errorf("formatting confirmation: %w", err)
			}

			pubUrlStr := serverCfg.PublicURL
			if pubUrlStr == "" {
				pubUrlStr = fmt.Sprintf("http://%s:%d", serverCfg.Host, serverCfg.Port)
			}
			validationURL, err := url.Parse(fmt.Sprintf("%s/validate-email?ucan=%s&mode=authorize", pubUrlStr, confirmationStr))
			if err != nil {
				log.Error("failed to parse validation URL", zap.Error(err))
				return fmt.Errorf("parsing validation URL: %w", err)
			}

			err = mailer.SendValidation(req.Context(), email, *validationURL)
			if err != nil {
				log.Error("failed to send validation email", zap.Error(err))
				return fmt.Errorf("sending validation email: %w", err)
			}

			return res.SetSuccess(&access.RequestOK{
				// link to this access request
				Request: req.Invocation().Task().Link(),
				// link to the authorization confirmation so it could be used to lookup
				// the delegation by the access request.
				Confirm: promise.AwaitOK{Task: confirmation.Task().Link()},
				// let client know when the confirmation will expire
				Expiration: int64(exp),
			})
		},
	)
}
