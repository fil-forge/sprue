package ucan_client

import (
	"context"
	"fmt"
	"reflect"

	"github.com/fil-forge/ucantone/client"
	edm "github.com/fil-forge/ucantone/errors/datamodel"
	"github.com/fil-forge/ucantone/execution"
	"github.com/fil-forge/ucantone/ipld"
	"github.com/fil-forge/ucantone/ipld/codec/dagcbor"
	"github.com/fil-forge/ucantone/ipld/datamodel"
	"github.com/fil-forge/ucantone/result"
	"github.com/fil-forge/ucantone/ucan"
	"go.uber.org/zap"
)

// Execute sends the given invocation using the provided client and decodes the
// response into the specified type.
func Execute[T dagcbor.Unmarshaler](
	ctx context.Context,
	client *client.HTTPClient,
	logger *zap.Logger,
	inv ucan.Invocation,
	options ...execution.RequestOption,
) (T, ucan.Receipt, error) {
	fields := []zap.Field{
		zap.Stringer("issuer", inv.Issuer().DID()),
		zap.Stringer("subject", inv.Subject().DID()),
		zap.Stringer("command", inv.Command()),
		zap.Any("arguments", inv.Arguments()),
	}
	if inv.Audience() != nil {
		fields = append(fields, zap.Stringer("audience", inv.Audience().DID()))
	}
	if len(inv.Metadata()) > 0 {
		fields = append(fields, zap.Any("metadata", inv.Metadata()))
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
	ok, err := result.MatchResultR2(
		rcpt.Out(),
		func(o ipld.Any) (T, error) {
			var ok T
			// if ok is a pointer type, then we need to create an instance of it
			// because rebind requires a non-nil pointer.
			typ := reflect.TypeOf(ok)
			if typ.Kind() == reflect.Ptr {
				ok = reflect.New(typ.Elem()).Interface().(T)
			}
			err := datamodel.Rebind(datamodel.NewAny(o), ok)
			if err != nil {
				log.Error("failed to bind invocation response", zap.Error(err))
				return zero, fmt.Errorf("binding invocation response: %w", err)
			}
			return ok, nil
		},
		func(x ipld.Any) (T, error) {
			var model edm.ErrorModel
			err := datamodel.Rebind(datamodel.NewAny(x), &model)
			if err != nil {
				log.Error("failed to bind execution failure", zap.Error(err))
				log.Error("failed execution", zap.Any("error", x))
				return zero, fmt.Errorf("executing invocation: %v", x)
			}
			log.Error("failed execution", zap.String("name", model.ErrorName), zap.Error(model))
			return zero, fmt.Errorf("executing invocation: %w", model)
		},
	)
	return ok, rcpt, err
}
