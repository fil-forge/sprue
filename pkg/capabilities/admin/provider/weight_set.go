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

const WeightSetAbility = "admin/provider/weight/set"

type WeightSetCaveats struct {
	Provider          did.DID
	Weight            int
	ReplicationWeight int
}

func (wc WeightSetCaveats) ToIPLD() (datamodel.Node, error) {
	return ipld.WrapWithRecovery(&wc, WeightSetCaveatsType(), types.Converters...)
}

type WeightSetOk = ok.Unit

var WeightSet = validator.NewCapability(
	WeightSetAbility,
	schema.DIDString(),
	schema.Struct[WeightSetCaveats](WeightSetCaveatsType(), nil, types.Converters...),
	validator.DefaultDerives[WeightSetCaveats],
)
