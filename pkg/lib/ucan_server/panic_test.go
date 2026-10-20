package ucan_server_test

import (
	"strings"
	"testing"

	uploadcmds "github.com/fil-forge/libforge/commands/upload"
	"github.com/fil-forge/sprue/internal/testutil"
	"github.com/fil-forge/sprue/pkg/lib/ucan_server"
	"github.com/fil-forge/ucantone/execution"
	"github.com/fil-forge/ucantone/ucan/invocation"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

func TestPanicLogger(t *testing.T) {
	inv, err := uploadcmds.Remove.Invoke(
		testutil.Alice,
		testutil.RandomIssuer(t).DID(),
		&uploadcmds.RemoveArguments{Root: testutil.RandomCID(t)},
		invocation.WithAudience(testutil.WebService.DID()),
	)
	require.NoError(t, err)

	core, logs := observer.New(zapcore.ErrorLevel)
	ucan_server.NewPanicLogger(zap.New(core))(execution.NewRequest(t.Context(), inv), "boom")

	entries := logs.All()
	require.Len(t, entries, 1)
	fields := entries[0].ContextMap()
	stack, _ := fields["stack"].(string)
	got := map[string]any{
		"message":        entries[0].Message,
		"task":           fields["task"],
		"command":        fields["command"],
		"panic":          fields["panic"],
		"stackHasCaller": strings.Contains(stack, "TestPanicLogger"),
	}
	require.Equal(t, map[string]any{
		"message":        "panic executing invocation",
		"task":           inv.Task().Link().String(),
		"command":        inv.Command().String(),
		"panic":          "boom",
		"stackHasCaller": true,
	}, got)
}
