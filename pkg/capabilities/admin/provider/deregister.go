package provider

import (
	"github.com/fil-forge/go-ucanto/core/ipld"
	"github.com/fil-forge/go-ucanto/core/result/ok"
	"github.com/fil-forge/go-ucanto/core/schema"
	"github.com/fil-forge/go-ucanto/did"
	"github.com/fil-forge/go-ucanto/validator"
	"github.com/ipld/go-ipld-prime/datamodel"

	"github.com/fil-forge/go-libstoracha/capabilities/types"
)

const DeregisterAbility = "admin/provider/deregister"

type DeregisterCaveats struct {
	Provider did.DID
}

func (dc DeregisterCaveats) ToIPLD() (datamodel.Node, error) {
	return ipld.WrapWithRecovery(&dc, DeregisterCaveatsType(), types.Converters...)
}

type DeregisterOk = ok.Unit

var Deregister = validator.NewCapability(
	DeregisterAbility,
	schema.DIDString(),
	schema.Struct[DeregisterCaveats](DeregisterCaveatsType(), nil, types.Converters...),
	validator.DefaultDerives[DeregisterCaveats],
)
