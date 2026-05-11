package service

import (
	"bytes"
	"errors"
	"fmt"
	"net/http"
	"slices"

	"github.com/fil-forge/sprue/pkg/identity"
	"github.com/fil-forge/sprue/pkg/lib/ucan_server"
	"github.com/fil-forge/sprue/pkg/service/handlers"
	"github.com/fil-forge/sprue/pkg/service/ui"
	"github.com/fil-forge/sprue/pkg/store/agent"
	delegation_store "github.com/fil-forge/sprue/pkg/store/delegation"
	"github.com/fil-forge/ucantone/ipld/codec/dagcbor"
	"github.com/fil-forge/ucantone/principal"
	"github.com/fil-forge/ucantone/server"
	"github.com/fil-forge/ucantone/ucan/container"
	"github.com/fil-forge/ucantone/validator"
	"github.com/ipfs/go-cid"
	"github.com/labstack/echo/v4"
	"go.uber.org/zap"
)

// Service implements the sprue upload service logic.
type Service struct {
	identity        *identity.Identity
	agentStore      agent.Store
	delegationStore delegation_store.Store
	logger          *zap.Logger
	ucanServer      *server.HTTPServer
}

// New creates a new Service instance.
func New(id *identity.Identity, agentStore agent.Store, delegationStore delegation_store.Store, handlers []handlers.Handler, logger *zap.Logger, options ...server.HTTPOption) *Service {
	return &Service{
		identity:        id,
		agentStore:      agentStore,
		delegationStore: delegationStore,
		logger:          logger,
		ucanServer:      createUCANServer(id.Signer, agentStore, handlers, logger, options...),
	}
}

// createUCANServer creates the UCAN RPC server with registered handlers.
func createUCANServer(id principal.Signer, agentStore agent.Store, handlers []handlers.Handler, logger *zap.Logger, options ...server.HTTPOption) *server.HTTPServer {
	options = append(
		slices.Clone(options),
		server.WithReceiptTimestamps(true),
		server.WithEventListener(ucan_server.AgentMessageLogger{Logger: logger, AgentStore: agentStore}),
		server.WithEventListener(ucan_server.ErrorHandler{Logger: logger}),
		server.WithValidationOptions(
			validator.WithPrincipalParser(ucan_server.PrincipalParser),
			validator.WithNonStandardSignatureVerifier(
				ucan_server.NewAttestationVerifier(id.Verifier()),
			),
		),
	)
	srv := server.NewHTTP(id, options...)
	for _, h := range handlers {
		srv.Handle(h.Capability, h.Handler)
	}
	return srv
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
	rcpt, err := s.agentStore.GetReceipt(c.Request().Context(), task)
	if err != nil {
		if errors.Is(err, agent.ErrReceiptNotFound) {
			return c.JSON(http.StatusNotFound, map[string]string{
				"error": "receipt not found",
			})
		}
		return fmt.Errorf("getting receipt: %w", err)
	}

	ct := container.New(container.WithReceipts(rcpt))
	var buf bytes.Buffer
	if err := ct.MarshalCBOR(&buf); err != nil {
		return fmt.Errorf("marshaling receipt container: %w", err)
	}

	return c.Blob(http.StatusOK, dagcbor.ContentType, buf.Bytes())
}
