package handlers

import (
	"go.uber.org/fx"

	"github.com/fil-forge/go-ucanto/ucan"
	"github.com/fil-forge/sprue/pkg/service/handlers"
)

var Module = fx.Module("service-handlers",
	fx.Provide(
		fx.Annotate(
			handlers.WithAccessAuthorizeMethod,
			fx.ResultTags(`group:"ucan_options"`),
		),
		fx.Annotate(
			handlers.WithAccessClaimMethod,
			fx.ResultTags(`group:"ucan_options"`),
		),
		fx.Annotate(
			handlers.WithAccessDelegateMethod,
			fx.ResultTags(`group:"ucan_options"`),
		),
		fx.Annotate(
			handlers.WithAdminProviderDeregisterMethod,
			fx.ResultTags(`group:"ucan_options"`),
		),
		fx.Annotate(
			handlers.WithAdminProviderListMethod,
			fx.ResultTags(`group:"ucan_options"`),
		),
		fx.Annotate(
			handlers.WithAdminProviderRegisterMethod,
			fx.ResultTags(`group:"ucan_options"`),
		),
		fx.Annotate(
			handlers.WithAdminProviderWeightSetMethod,
			fx.ResultTags(`group:"ucan_options"`),
		),
		fx.Annotate(
			handlers.WithFilecoinOfferMethod,
			fx.ResultTags(`group:"ucan_options"`),
		),
		fx.Annotate(
			handlers.WithProviderAddMethod,
			fx.ResultTags(`group:"ucan_options"`),
		),
		fx.Annotate(
			handlers.WithSpaceBlobAddMethod,
			fx.ResultTags(`group:"ucan_options"`),
		),
		fx.Annotate(
			handlers.WithSpaceBlobReplicateMethod,
			fx.ResultTags(`group:"ucan_options"`),
		),
		fx.Annotate(
			handlers.WithSpaceInfoMethod,
			fx.ResultTags(`group:"ucan_options"`),
		),
		fx.Annotate(
			handlers.WithSpaceIndexAddMethod,
			fx.ResultTags(`group:"ucan_options"`),
		),
		fx.Annotate(
			handlers.WithUCANConcludeMethod,
			fx.ResultTags(`group:"ucan_options"`),
		),
		fx.Annotate(
			handlers.WithUploadAddMethod,
			fx.ResultTags(`group:"ucan_options"`),
		),
		fx.Annotate(
			handlers.WithUploadListMethod,
			fx.ResultTags(`group:"ucan_options"`),
		),
		fx.Annotate(
			handlers.WithUploadShardListMethod,
			fx.ResultTags(`group:"ucan_options"`),
		),
		fx.Annotate(
			handlers.NewHTTPPutConcludeHandler,
			fx.ResultTags(`group:"ucan_conclude_handlers"`),
		),
		fx.Annotate(
			handlers.NewBlobReplicaTransferConcludeHandler,
			fx.ResultTags(`group:"ucan_conclude_handlers"`),
		),
		NewConcludeHandlers,
	),
)

type ConcludeHandlersParams struct {
	fx.In
	Handlers []handlers.ConclusionHandler `group:"ucan_conclude_handlers"`
}

func NewConcludeHandlers(params ConcludeHandlersParams) map[ucan.Ability]handlers.ConclusionHandlerFunc {
	handlers := make(map[ucan.Ability]handlers.ConclusionHandlerFunc, len(params.Handlers))
	for _, h := range params.Handlers {
		handlers[h.Ability] = h.Handler
	}
	return handlers
}
