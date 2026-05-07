package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"

	"github.com/fil-forge/go-libstoracha/capabilities/access"
	"github.com/fil-forge/go-ucanto/core/car"
	"github.com/fil-forge/go-ucanto/core/dag/blockstore"
	"github.com/fil-forge/go-ucanto/core/delegation"
	"github.com/fil-forge/go-ucanto/core/ipld"
	"github.com/fil-forge/go-ucanto/core/message"
	"github.com/fil-forge/go-ucanto/core/receipt"
	"github.com/fil-forge/go-ucanto/core/result"
	"github.com/fil-forge/go-ucanto/server"
	ucanhttp "github.com/fil-forge/go-ucanto/transport/http"
	"github.com/fil-forge/go-ucanto/ucan"
	"github.com/fil-forge/go-ucanto/validator"
	"github.com/ipfs/go-cid"
	"github.com/labstack/echo/v4"
	"go.uber.org/zap"

	"github.com/fil-forge/sprue/pkg/identity"
	"github.com/fil-forge/sprue/pkg/indexerclient"
	"github.com/fil-forge/sprue/pkg/lib/didmailto"
	"github.com/fil-forge/sprue/pkg/lib/ucans"
	"github.com/fil-forge/sprue/pkg/service/handlers"
	"github.com/fil-forge/sprue/pkg/service/ui"
	"github.com/fil-forge/sprue/pkg/store/agent"
	delegation_store "github.com/fil-forge/sprue/pkg/store/delegation"
)

// Service implements the sprue upload service logic.
type Service struct {
	identity        *identity.Identity
	agentStore      agent.Store
	delegationStore delegation_store.Store
	indexerClient   *indexerclient.Client
	logger          *zap.Logger
	ucanServer      server.ServerView[server.Service]
	options         []server.Option
}

// New creates a new Service instance.
func New(id *identity.Identity, agentStore agent.Store, delegationStore delegation_store.Store, indexerClient *indexerclient.Client, logger *zap.Logger, options ...server.Option) (*Service, error) {
	svc := &Service{
		identity:        id,
		agentStore:      agentStore,
		delegationStore: delegationStore,
		indexerClient:   indexerClient,
		logger:          logger,
		options:         options,
	}

	// Create UCAN server with handlers
	ucanSrv, err := svc.createUCANServer()
	if err != nil {
		return nil, fmt.Errorf("failed to create UCAN server: %w", err)
	}
	svc.ucanServer = ucanSrv

	return svc, nil
}

// createUCANServer creates the UCAN RPC server with registered handlers.
func (s *Service) createUCANServer() (server.ServerView[server.Service], error) {
	log := s.logger
	options := append(
		slices.Clone(s.options),
		server.WithErrorHandler(func(err server.HandlerExecutionError[any]) {
			if stack := err.Stack(); stack != "" {
				log = log.With(zap.String("stack", stack))
			}
			log.Error("handler execution", zap.Error(err))
		}),
	)
	return server.NewServer(s.identity.Signer, options...)
}

// HandleUCANRequest handles incoming UCAN RPC requests.
func (s *Service) HandleUCANRequest(c echo.Context) error {
	r := c.Request()

	inBytes, inMsg, inIdx, err := decodeAndIndex(r.Body)
	if err != nil {
		return fmt.Errorf("decoding and indexing incoming agent message: %w", err)
	}
	r.Body.Close()

	err = s.agentStore.Write(r.Context(), inMsg, inIdx, inBytes)
	if err != nil {
		return fmt.Errorf("writing incoming agent message to agent store: %w", err)
	}

	res, err := s.ucanServer.Request(r.Context(), ucanhttp.NewRequest(bytes.NewReader(inBytes), r.Header))
	if err != nil {
		s.logger.Error("UCAN request error", zap.Error(err))
		return fmt.Errorf("handling UCAN request: %w", err)
	}

	outBytes, outMsg, outIdx, err := decodeAndIndex(res.Body())
	if err != nil {
		return fmt.Errorf("decoding and indexing outgoing agent message: %w", err)
	}
	res.Body().Close()

	err = s.agentStore.Write(r.Context(), outMsg, outIdx, outBytes)
	if err != nil {
		return fmt.Errorf("writing outgoing agent message to agent store: %w", err)
	}

	// Copy response headers
	for key, vals := range res.Headers() {
		for _, v := range vals {
			c.Response().Header().Add(key, v)
		}
	}

	return c.Stream(res.Status(), "", bytes.NewReader(outBytes))
}

func decodeAndIndex(r io.Reader) ([]byte, message.AgentMessage, []agent.IndexEntry, error) {
	body, err := io.ReadAll(r)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("reading request body: %w", err)
	}
	roots, blocks, err := car.Decode(bytes.NewReader(body))
	if err != nil {
		return nil, nil, nil, fmt.Errorf("decoding CAR: %w", err)
	}
	if len(roots) != 1 {
		return nil, nil, nil, fmt.Errorf("expected exactly one root in CAR, got %d", len(roots))
	}
	br, err := blockstore.NewBlockReader(blockstore.WithBlocksIterator(blocks))
	if err != nil {
		return nil, nil, nil, fmt.Errorf("creating block reader: %w", err)
	}
	msg, err := message.NewMessage(roots[0], br)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("creating agent message: %w", err)
	}
	var entries []agent.IndexEntry
	for ent, err := range agent.Index(msg) {
		if err != nil {
			return nil, nil, nil, fmt.Errorf("indexing agent message: %w", err)
		}
		entries = append(entries, ent)
	}
	return body, msg, entries, nil
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
		res, err := s.authorize(c.Request().Context(), c.QueryParam("ucan"))
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

type authorizationResult struct {
	Email    string
	Audience string
	UCAN     string
	Facts    []ucan.Fact
}

func (s *Service) authorize(ctx context.Context, ucan string) (authorizationResult, error) {
	dlgs, err := ucans.ParseDelegations(ucan)
	if err != nil {
		return authorizationResult{}, fmt.Errorf("parsing delegations: %w", err)
	}
	if len(dlgs) != 1 {
		return authorizationResult{}, fmt.Errorf("unexpected number of delegations found in UCAN")
	}
	confirmation := dlgs[0]

	confirm := server.Provide(
		access.Confirm,
		handlers.AccessConfirmHandler(s.identity, s.delegationStore, s.logger),
	)
	txn, err := confirm(ctx, confirmation, s.ucanServer.Context())
	if err != nil {
		return authorizationResult{}, fmt.Errorf("executing access/confirm handler: %w", err)
	}
	o, x := result.Unwrap(txn.Out())
	if x != nil {
		return authorizationResult{}, fmt.Errorf("access/confirm invocation failure: %w", x)
	}

	// Extract the email and audience from the confirmation invocation.
	// This should match since we just successfully invoked the handler.
	match, err := access.Confirm.Match(validator.NewSource(confirmation.Capabilities()[0], confirmation))
	if err != nil {
		return authorizationResult{}, fmt.Errorf("matching access/confirm capability: %w", err)
	}
	email, err := didmailto.Email(match.Value().Nb().Iss)
	if err != nil {
		return authorizationResult{}, fmt.Errorf("parsing account DID: %w", err)
	}

	var confirmDlgs []delegation.Delegation
	for _, bytes := range o.Delegations.Values {
		dlgs, err := ucans.ExtractDelegations(bytes)
		if err != nil {
			return authorizationResult{}, fmt.Errorf("extracting delegations from confirmation result: %w", err)
		}
		if len(dlgs) != 1 {
			return authorizationResult{}, fmt.Errorf("unexpected number of delegations found in confirmation result")
		}
		confirmDlgs = append(confirmDlgs, dlgs[0])
	}

	ucan, err = ucans.FormatDelegations(confirmDlgs...)
	if err != nil {
		return authorizationResult{}, fmt.Errorf("formatting delegations: %w", err)
	}

	return authorizationResult{
		Email:    email,
		Audience: match.Value().Nb().Aud.String(),
		UCAN:     ucan,
		Facts:    confirmation.Facts(),
	}, nil
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

	// Build an agent message containing the receipt
	msg, err := message.Build(nil, []receipt.AnyReceipt{rcpt})
	if err != nil {
		s.logger.Error("failed to build message", zap.Error(err))
		return c.JSON(http.StatusInternalServerError, map[string]string{
			"error": "failed to build message",
		})
	}

	reader := car.Encode([]ipld.Link{msg.Root().Link()}, msg.Blocks())
	return c.Stream(http.StatusOK, car.ContentType, reader)
}
