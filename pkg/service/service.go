package service

import (
	"bytes"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"time"

	"github.com/fil-forge/libforge/attestation"
	"github.com/fil-forge/libforge/attestation/didmailto"
	"github.com/fil-forge/libforge/identity"
	"github.com/fil-forge/sprue/pkg/lib/ucan_server"
	"github.com/fil-forge/sprue/pkg/service/ui"
	"github.com/fil-forge/sprue/pkg/store/agent"
	delegation_store "github.com/fil-forge/sprue/pkg/store/delegation"
	"github.com/fil-forge/ucantone/did"
	"github.com/fil-forge/ucantone/did/key"
	"github.com/fil-forge/ucantone/did/plc"
	"github.com/fil-forge/ucantone/did/resolver"
	"github.com/fil-forge/ucantone/did/web"
	"github.com/fil-forge/ucantone/ipld/codec/dagcbor"
	"github.com/fil-forge/ucantone/multikey"

	// Registers the secp256k1 verifier decoder: did:plc issuers (tenants)
	// carry secp256k1 verification methods.
	_ "github.com/fil-forge/ucantone/multikey/secp256k1/verifier"
	"github.com/fil-forge/ucantone/server"
	"github.com/fil-forge/ucantone/ucan"
	"github.com/fil-forge/ucantone/ucan/container"
	"github.com/fil-forge/ucantone/validator"
	"github.com/ipfs/go-cid"
	"github.com/labstack/echo/v4"
	"go.uber.org/zap"
)

type serviceConfig struct {
	serverOptions         []server.HTTPOption
	insecureDIDResolution bool
	plcDirectory          string
}

type Option func(*serviceConfig)

// WithServerOptions allows passing custom options to the underlying UCAN server.
func WithServerOptions(opts ...server.HTTPOption) Option {
	return func(c *serviceConfig) {
		c.serverOptions = opts
	}
}

// WithInsecureDIDResolution enables HTTP (instead of HTTPS) for did:web
// resolution, which should only be used for development purposes.
func WithInsecureDIDResolution(enabled bool) Option {
	return func(c *serviceConfig) {
		c.insecureDIDResolution = enabled
	}
}

// WithPLCDirectory sets the did:plc directory endpoint used to resolve
// did:plc issuers (e.g. tenants invoking /provider/add during bucket
// provisioning). Empty leaves did:plc unresolvable.
func WithPLCDirectory(directory string) Option {
	return func(c *serviceConfig) {
		c.plcDirectory = directory
	}
}

// Service implements the sprue upload service logic.
type Service struct {
	identity        identity.Identity
	agentStore      agent.Store
	delegationStore delegation_store.Store
	logger          *zap.Logger
	ucanServer      *server.HTTPServer
}

// New creates a new Service instance.
func New(id identity.Identity, agentStore agent.Store, delegationStore delegation_store.Store, handlers []server.Route, logger *zap.Logger, options ...Option) (*Service, error) {
	server, err := createUCANServer(id.Issuer, agentStore, handlers, logger, options...)
	if err != nil {
		return nil, err
	}
	return &Service{
		identity:        id,
		agentStore:      agentStore,
		delegationStore: delegationStore,
		logger:          logger,
		ucanServer:      server,
	}, nil
}

// createUCANServer creates the UCAN RPC server with registered handlers.
func createUCANServer(id multikey.Issuer, agentStore agent.Store, handlers []server.Route, logger *zap.Logger, options ...Option) (*server.HTTPServer, error) {
	cfg := serviceConfig{}
	for _, opt := range options {
		opt(&cfg)
	}

	webResolverOpts := []web.Option{}
	if cfg.insecureDIDResolution {
		logger.Warn("insecure DID resolution enabled: did:web will be resolved over HTTP instead of HTTPS; this should only be used for development purposes")
		webResolverOpts = append(webResolverOpts, web.WithInsecure(true))
	}
	webResolver, err := web.NewResolver(webResolverOpts...)
	if err != nil {
		return nil, err
	}

	selfDoc, err := identity.Identity{Issuer: id}.DIDDocument()
	if err != nil {
		return nil, fmt.Errorf("creating DID document for service identity: %w", err)
	}

	// did:plc resolution is enabled when a directory endpoint is configured —
	// needed to verify did:plc issuers (e.g. tenants signing /provider/add
	// invocations during bucket provisioning).
	var plcResolver did.Resolver
	if cfg.plcDirectory != "" {
		u, err := url.Parse(cfg.plcDirectory)
		if err != nil {
			return nil, fmt.Errorf("parsing PLC directory URL %q: %w", cfg.plcDirectory, err)
		}
		p, err := plc.NewResolver(*u)
		if err != nil {
			return nil, fmt.Errorf("creating did:plc resolver: %w", err)
		}
		plcResolver = resolver.NewCached(p, time.Hour*3)
	}

	resolver := resolver.ByMethod{
		"key": key.Resolver,
		"web": resolver.Tiered{
			resolver.WellKnown{id.DID(): selfDoc},
			resolver.NewCached(webResolver, time.Hour*3),
		},
		"mailto": didmailto.NewResolver(id.DID()),
	}
	if plcResolver != nil {
		resolver["plc"] = plcResolver
	}

	factories := validator.DefaultFactories()
	factories[attestation.Type] = attestation.NewVerifierFactory(resolver, factories)

	serverOpts := append(
		slices.Clone(cfg.serverOptions),
		server.WithReceiptTimestamps(true),
		server.WithEventListener(&ucan_server.AgentMessageLogger{Logger: logger, AgentStore: agentStore}),
		server.WithEventListener(&ucan_server.ErrorHandler{Logger: logger}),
		server.WithValidationOptions(
			validator.WithDIDResolver(resolver),
			validator.WithVerifierFactories(factories),
		),
	)

	srv := server.NewHTTP(id, serverOpts...)
	for _, h := range handlers {
		srv.Handle(h.Command, h.Handler)
	}
	return srv, nil
}

// HandleUCANRequest handles incoming UCAN RPC requests.
func (s *Service) HandleUCANRequest(c echo.Context) error {
	s.ucanServer.ServeHTTP(c.Response(), c.Request())
	return nil
}

func (s *Service) HandleValidateEmailRequest(c echo.Context) error {
	if c.QueryParam("ucan") == "" {
		r, err := ui.ErrorPage("missing ucan query parameter")
		if err != nil {
			return fmt.Errorf("failed to render error page: %w", err)
		}
		return c.Stream(http.StatusBadRequest, "text/html", r)
	}

	switch c.Request().Method {
	case http.MethodGet:
		r, err := ui.PendingValidateEmailPage(true)
		if err != nil {
			return fmt.Errorf("failed to render validation page: %w", err)
		}
		return c.Stream(http.StatusOK, "text/html", r)
	case http.MethodPost:
		res, err := ucan_server.ExecBase64urlAccessConfirm(c.Request().Context(), s.ucanServer, c.QueryParam("ucan"))
		if err != nil {
			s.logger.Error("authorization error", zap.Error(err))
			r, err := ui.ErrorPage(fmt.Sprintf("Oops, something went wrong: %s", err.Error()))
			if err != nil {
				return fmt.Errorf("failed to render error page: %w", err)
			}
			return c.Stream(http.StatusInternalServerError, "text/html", r)
		}
		r, err := ui.ValidateEmailPage(res.UCAN, res.Email, res.Audience)
		if err != nil {
			return fmt.Errorf("failed to render validation page: %w", err)
		}
		return c.Stream(http.StatusOK, "text/html", r)
	default:
		return c.String(http.StatusMethodNotAllowed, "method not allowed")
	}
}

// HandleReceiptRequest handles receipt retrieval requests.
func (s *Service) HandleReceiptRequest(c echo.Context) error {
	task, err := cid.Parse(c.Param("cid"))
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{
			"error": fmt.Sprintf("invalid task CID: %v", err),
		})
	}

	s.logger.Debug("receipt request", zap.String("task", task.String()))
	page, err := s.agentStore.List(c.Request().Context(), task, agent.WithListLimit(25))
	if err != nil {
		return fmt.Errorf("listing agent messages: %w", err)
	}

	found := false
	var invs []ucan.Invocation
	var dlgs []ucan.Delegation
	var rcpts []ucan.Receipt
	for _, msg := range page.Results {
		invs = append(invs, msg.Invocations()...)
		dlgs = append(dlgs, msg.Delegations()...)
		rcpts = append(rcpts, msg.Receipts()...)
		for _, r := range msg.Receipts() {
			if r.Ran() == task {
				found = true
			}
		}
	}
	if !found {
		return c.JSON(http.StatusNotFound, map[string]string{
			"error": "receipt not found",
		})
	}

	ct := container.New(
		container.WithInvocations(invs...),
		container.WithDelegations(dlgs...),
		container.WithReceipts(rcpts...),
	)
	var buf bytes.Buffer
	if err := ct.MarshalCBOR(&buf); err != nil {
		return fmt.Errorf("marshaling receipt container: %w", err)
	}

	return c.Blob(http.StatusOK, dagcbor.ContentType, buf.Bytes())
}
