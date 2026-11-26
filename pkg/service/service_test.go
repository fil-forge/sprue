package service

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	blobcmds "github.com/fil-forge/libforge/commands/blob"
	"github.com/fil-forge/sprue/internal/testutil"
	"github.com/fil-forge/sprue/pkg/store/agent"
	agentmemory "github.com/fil-forge/sprue/pkg/store/agent/memory"
	"github.com/fil-forge/ucantone/execution"
	"github.com/fil-forge/ucantone/ipld/codec/dagcbor"
	"github.com/fil-forge/ucantone/ipld/datamodel"
	"github.com/fil-forge/ucantone/server"
	"github.com/fil-forge/ucantone/ucan"
	"github.com/fil-forge/ucantone/ucan/command"
	"github.com/fil-forge/ucantone/ucan/container"
	"github.com/fil-forge/ucantone/ucan/invocation"
	"github.com/fil-forge/ucantone/ucan/promise"
	"github.com/fil-forge/ucantone/ucan/receipt"
	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

var echoCommand = command.MustParse("/test/echo")

// echoServer is the service's UCAN server over the given agent store, with
// one handler that succeeds.
func echoServer(t *testing.T, agentStore agent.Store) *server.HTTPServer {
	t.Helper()
	route := server.NewRoute(echoCommand, func(req execution.Request, res execution.Response) error {
		return res.SetSuccess(datamodel.Map{})
	})
	srv, err := createUCANServer(testutil.Service, agentStore, []server.Route{route}, zaptest.NewLogger(t))
	require.NoError(t, err)
	return srv
}

// roundTrip sends one echo invocation through the server and returns it,
// having checked the response is a success.
func roundTrip(t *testing.T, srv *server.HTTPServer) ucan.Invocation {
	t.Helper()
	inv, err := invocation.Invoke(testutil.Alice, testutil.Alice.DID(), echoCommand, datamodel.Map{}, invocation.WithAudience(testutil.Service.DID()))
	require.NoError(t, err)
	ct := container.New(container.WithInvocations(inv))
	r, w := io.Pipe()
	go func() { w.CloseWithError(ct.MarshalCBOR(w)) }()
	req := (&http.Request{Header: http.Header{}, Body: r}).WithContext(t.Context())
	req.Header.Set("Content-Type", dagcbor.ContentType)

	resp, err := srv.RoundTrip(req)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	return inv
}

// The request and its receipt are both in the agent store by the time the
// response is returned, even though the request is stored while the handler
// runs rather than before it.
func TestUCANServerStoresRequestAndResponse(t *testing.T) {
	agentStore := agentmemory.New()
	inv := roundTrip(t, echoServer(t, agentStore))

	task := inv.Task().Link()
	got, err := agentStore.GetInvocation(t.Context(), task)
	require.NoError(t, err)
	require.Equal(t, inv.Link(), got.Link())
	rcpt, err := agentStore.GetReceipt(t.Context(), task)
	require.NoError(t, err)
	_, x := rcpt.Out().Unpack()
	require.Nil(t, x)
}

// A failed write of the incoming message does not fail the request: by the
// time it is known the handlers have run, and nothing reads the record on a
// request path. The receipt is still stored.
func TestUCANServerToleratesLostRequestRecord(t *testing.T) {
	agentStore := &requestWriteFails{Store: agentmemory.New()}
	inv := roundTrip(t, echoServer(t, agentStore))

	task := inv.Task().Link()
	_, err := agentStore.GetInvocation(t.Context(), task)
	require.ErrorIs(t, err, agent.ErrInvocationNotFound)
	_, err = agentStore.GetReceipt(t.Context(), task)
	require.NoError(t, err)
}

// requestWriteFails is an agent store whose writes of incoming messages,
// the ones carrying no receipts, fail.
type requestWriteFails struct {
	agent.Store
}

func (s *requestWriteFails) Write(ctx context.Context, message ucan.Container, index []agent.IndexEntry) error {
	if len(message.Receipts()) == 0 {
		return errors.New("store unavailable")
	}
	return s.Store.Write(ctx, message, index)
}

func writeMessage(t *testing.T, store agent.Store, invs []ucan.Invocation, rcpts []ucan.Receipt) {
	t.Helper()
	msg := container.New(container.WithInvocations(invs...), container.WithReceipts(rcpts...))
	require.NoError(t, store.Write(t.Context(), msg, agent.Index(msg)))
}

// An accept receipt is no use to a client without the location commitment it
// names. The endpoint serves the two together even when other messages under
// the accept task crowd the commitment out of the page it lists.
func TestReceiptEndpointServesAcceptReceiptWithItsLocationCommitment(t *testing.T) {
	store := agentmemory.New()
	node := testutil.Bob

	invoke := func(cmd string) ucan.Invocation {
		return testutil.Must(invocation.Invoke(node, node.DID(), command.MustParse(cmd), datamodel.Map{}))(t)
	}
	accInv, claim, pdp := invoke("/blob/accept"), invoke("/assert/location"), invoke("/pdp/accept")
	task := accInv.Task().Link()
	accRcpt := testutil.Must(receipt.IssueOK(node, task, &blobcmds.AcceptOK{
		Site: claim.Link(),
		PDP:  promise.AwaitOK{Task: pdp.Task().Link()},
	}))(t)
	writeMessage(t, store, []ucan.Invocation{accInv, claim, pdp}, []ucan.Receipt{accRcpt})

	listsCommitment := func() bool {
		page, err := store.List(t.Context(), task, agent.WithListLimit(25))
		require.NoError(t, err)
		for _, msg := range page.Results {
			for _, inv := range msg.Invocations() {
				if inv.Link() == claim.Link() {
					return true
				}
			}
		}
		return false
	}
	// Each reply differs by one invocation, as each replayed
	// /blob/add differs by the add it answers.
	for listsCommitment() {
		writeMessage(t, store, []ucan.Invocation{accInv, invoke("/test/reply")}, []ucan.Receipt{accRcpt})
	}

	req := httptest.NewRequest(http.MethodGet, "/receipt/"+task.String(), nil)
	rec := httptest.NewRecorder()
	c := echo.New().NewContext(req, rec)
	c.SetParamNames("cid")
	c.SetParamValues(task.String())
	svc := &Service{agentStore: store, logger: zaptest.NewLogger(t)}
	require.NoError(t, svc.HandleReceiptRequest(c))
	require.Equal(t, http.StatusOK, rec.Code)

	var got container.Container
	require.NoError(t, got.UnmarshalCBOR(rec.Body))
	gotRcpt, ok := got.Receipt(task)
	require.True(t, ok, "no receipt for the accept task")
	require.Equal(t, accRcpt.Link().String(), gotRcpt.Link().String())
	// The listed page's invocations, and the two the receipt names.
	wantInvs := []string{claim.Link().String(), pdp.Link().String()}
	page, err := store.List(t.Context(), task, agent.WithListLimit(25))
	require.NoError(t, err)
	seen := map[string]bool{}
	for _, msg := range page.Results {
		for _, inv := range msg.Invocations() {
			if l := inv.Link().String(); !seen[l] {
				seen[l] = true
				wantInvs = append(wantInvs, l)
			}
		}
	}
	var gotInvs []string
	for _, inv := range got.Invocations() {
		gotInvs = append(gotInvs, inv.Link().String())
	}
	require.ElementsMatch(t, wantInvs, gotInvs)
}
