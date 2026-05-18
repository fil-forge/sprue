package ucan_client

import (
	"bytes"
	"context"
	"fmt"
	"reflect"

	"github.com/fil-forge/sprue/pkg/lib/zapipld"
	"github.com/fil-forge/ucantone/client"
	edm "github.com/fil-forge/ucantone/errors/datamodel"
	"github.com/fil-forge/ucantone/execution"
	"github.com/fil-forge/ucantone/ucan"
	cbg "github.com/whyrusleeping/cbor-gen"
	"go.uber.org/zap"
)

// Execute sends the given invocation using the provided client and decodes the
// response into the specified type.
func Execute[T cbg.CBORUnmarshaler](
	ctx context.Context,
	client *client.HTTPClient,
	logger *zap.Logger,
	inv ucan.Invocation,
	options ...execution.RequestOption,
) (T, ucan.Receipt, error) {
	fields := []zap.Field{
		zap.Stringer("issuer", inv.Issuer()),
		zap.Stringer("subject", inv.Subject()),
		zap.Stringer("command", inv.Command()),
		zap.Object("arguments", zapipld.RawMap(inv.ArgumentsBytes())),
	}
	if inv.Audience().Defined() {
		fields = append(fields, zap.Stringer("audience", inv.Audience()))
	}
	if len(inv.MetadataBytes()) > 0 {
		fields = append(fields, zap.Object("metadata", zapipld.RawMap(inv.MetadataBytes())))
	}
	if len(inv.Proofs()) > 0 {
		fields = append(fields, zap.Stringers("proofs", inv.Proofs()))
	}
	log := logger.With(zap.Dict("invocation", fields...))
	log.Debug("executing invocation")

	var zero T
	resp, err := client.Execute(execution.NewRequest(ctx, inv, options...))
	if err != nil {
		log.Error("failed to execute invocation", zap.Error(err))
		return zero, nil, fmt.Errorf("executing invocation: %w", err)
	}

	rcpt := resp.Receipt()

	o, x := rcpt.Out().Unpack()
	if rcpt.Out().IsErr() {
		var model edm.ErrorModel
		if err := model.UnmarshalCBOR(bytes.NewReader(x)); err != nil {
			log.Error("failed to unmarshal execution failure", zap.Error(err), zap.Binary("input", x))
			return zero, nil, fmt.Errorf("executing invocation")
		}
		log.Error("failed execution", zap.String("name", model.ErrorName), zap.Error(model))
		return zero, nil, fmt.Errorf("executing invocation: %w", model)
	}

	// if ok is a pointer type, allocate the underlying value so
	// UnmarshalCBOR has a non-nil pointer to write into.
	var ok T
	typ := reflect.TypeOf(ok)
	if typ.Kind() == reflect.Ptr {
		ok = reflect.New(typ.Elem()).Interface().(T)
	}
	if err := ok.UnmarshalCBOR(bytes.NewReader(o)); err != nil {
		log.Error("failed to unmarshal invocation response", zap.Error(err), zap.Binary("input", o))
		return zero, nil, fmt.Errorf("unmarshaling invocation response: %w", err)
	}
	return ok, rcpt, nil
}
