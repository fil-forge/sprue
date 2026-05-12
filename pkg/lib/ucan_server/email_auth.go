package ucan_server

import (
	"bytes"
	"context"
	"fmt"

	"github.com/fil-forge/libforge/capabilities/access"
	"github.com/fil-forge/libforge/didmailto"
	"github.com/fil-forge/ucantone/execution"
	"github.com/fil-forge/ucantone/ucan"
	"github.com/fil-forge/ucantone/ucan/container"
)

type AccessConfirmResult struct {
	Email    string
	Audience string
	UCAN     string
	Meta     []byte
}

// ExecBase64urlAccessConfirm executes an /access/confirm UCAN invocation
// contained in a base64url-encoded container string. Typically used by the
// email authentication flow.
func ExecBase64urlAccessConfirm(ctx context.Context, executor execution.Executor, input string) (AccessConfirmResult, error) {
	inCt, err := container.Decode([]byte(input))
	if err != nil {
		return AccessConfirmResult{}, fmt.Errorf("decoding UCAN container: %w", err)
	}
	if len(inCt.Invocations()) != 1 {
		return AccessConfirmResult{}, fmt.Errorf("unexpected number of invocations found in UCAN")
	}

	confirmation := inCt.Invocations()[0]
	// check this is a confirmation invocation
	if confirmation.Command() != access.ConfirmCommand {
		return AccessConfirmResult{}, fmt.Errorf("unexpected command in invocation, expected %s but got %s", access.ConfirmCommand, confirmation.Command())
	}

	req := execution.NewRequest(
		ctx,
		confirmation,
		execution.WithDelegations(inCt.Delegations()...),
		execution.WithReceipts(inCt.Receipts()...),
	)

	res, err := executor.Execute(req)
	if err != nil {
		return AccessConfirmResult{}, fmt.Errorf("executing confirm task %s: %w", confirmation.Task().Link(), err)
	}

	_, x := res.Receipt().Out().Unpack()
	if x != nil {
		return AccessConfirmResult{}, fmt.Errorf("invocation failure: %v", x)
	}

	confirmArgs := access.ConfirmArguments{}
	if err := confirmArgs.UnmarshalCBOR(bytes.NewReader(confirmation.ArgumentsBytes())); err != nil {
		return AccessConfirmResult{}, fmt.Errorf("unmarshaling confirmation arguments: %w", err)
	}

	email, err := didmailto.Email(confirmArgs.Issuer)
	if err != nil {
		return AccessConfirmResult{}, fmt.Errorf("parsing account DID: %w", err)
	}

	var invocations []ucan.Invocation
	var delegations []ucan.Delegation
	receipts := []ucan.Receipt{res.Receipt()}
	if res.Metadata() != nil {
		invocations = append(invocations, res.Metadata().Invocations()...)
		delegations = append(delegations, res.Metadata().Delegations()...)
		receipts = append(receipts, res.Metadata().Receipts()...)
	}

	outCt := container.New(
		container.WithInvocations(invocations...),
		container.WithDelegations(delegations...),
		container.WithReceipts(receipts...),
	)

	output, err := container.Encode(container.Base64urlGzip, outCt)
	if err != nil {
		return AccessConfirmResult{}, fmt.Errorf("encoding output UCAN container: %w", err)
	}

	return AccessConfirmResult{
		Email:    email,
		Audience: confirmArgs.Audience.String(),
		UCAN:     string(output),
		Meta:     confirmation.MetadataBytes(),
	}, nil
}
