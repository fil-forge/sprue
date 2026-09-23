package ucan_server_test

import (
	"testing"

	uploadcmds "github.com/fil-forge/libforge/commands/upload"
	"github.com/fil-forge/sprue/internal/testutil"
	"github.com/fil-forge/sprue/pkg/lib/ucan_server"
	"github.com/fil-forge/ucantone/ipld/datamodel"
	"github.com/fil-forge/ucantone/ucan/container"
	"github.com/fil-forge/ucantone/ucan/invocation"
	"github.com/fil-forge/ucantone/ucan/receipt"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

func TestErrorHandlerIgnoresFailureWithoutName(t *testing.T) {
	inv, err := uploadcmds.Remove.Invoke(
		testutil.Alice,
		testutil.RandomIssuer(t).DID(),
		&uploadcmds.RemoveArguments{Root: testutil.RandomCID(t)},
		invocation.WithAudience(testutil.WebService.DID()),
	)
	require.NoError(t, err)
	rcpt, err := receipt.IssueErr(testutil.WebService, inv.Task().Link(), datamodel.Map{"message": "no name"})
	require.NoError(t, err)
	ct := container.New(container.WithInvocations(inv), container.WithReceipts(rcpt))

	handler := ucan_server.ErrorHandler{Logger: zaptest.NewLogger(t)}
	require.NoError(t, handler.OnResponseEncode(t.Context(), ct))
}
