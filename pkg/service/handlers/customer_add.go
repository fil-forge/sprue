package handlers

import (
	"fmt"

	customercmds "github.com/fil-forge/libforge/commands/customer"
	"github.com/fil-forge/libforge/identity"
	customerstore "github.com/fil-forge/sprue/pkg/store/customer"
	"github.com/fil-forge/ucantone/binding"
	"github.com/fil-forge/ucantone/errors"
	"github.com/fil-forge/ucantone/server"
	"go.uber.org/zap"
)

// InvalidCustomerSubjectErrorName is the error name returned when a
// /customer/add invocation's subject is not the service DID.
const InvalidCustomerSubjectErrorName = "InvalidCustomerSubject"

// errInvalidCustomerSubject is returned when the invocation subject is not the
// service DID.
var errInvalidCustomerSubject = errors.New(InvalidCustomerSubjectErrorName, "invocation subject must be the service")

// NewCustomerAddHandler handles /customer/add invocations. It asserts that the
// invocation subject is the service DID and registers the customer in the
// customer store.
func NewCustomerAddHandler(id identity.Identity, customerStore customerstore.Store, logger *zap.Logger) server.Route {
	log := logger.With(zap.Stringer("handler", customercmds.Add))
	return customercmds.Add.Route(
		func(req *binding.Request[*customercmds.AddArguments], res *binding.Response[*customercmds.AddOK]) error {
			if req.Invocation().Subject() != id.DID() {
				log.Warn("not a valid invocation", zap.Stringer("subject", req.Invocation().Subject()))
				return res.SetFailure(errInvalidCustomerSubject)
			}

			args := req.Task().Arguments()

			log := log.With(
				zap.Stringer("customer", args.Customer),
				zap.Stringer("product", args.Product),
			)
			log.Debug("adding customer")

			var details map[string]any
			if len(args.Details) > 0 {
				details = make(map[string]any, len(args.Details))
				for k, v := range args.Details {
					details[k] = v
				}
			}

			err := customerStore.Add(req.Context(), args.Customer, args.Account, args.Product, details, nil)
			if err != nil {
				if errors.Is(err, customerstore.ErrCustomerExists) {
					log.Warn("customer already exists")
					return res.SetFailure(err)
				}
				log.Error("failed to add customer", zap.Error(err))
				return fmt.Errorf("adding customer: %w", err)
			}

			log.Debug("customer added successfully")
			return res.SetSuccess(&customercmds.AddOK{})
		},
	)
}
