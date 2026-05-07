package provider

import (
	"github.com/fil-forge/go-ucanto/core/ipld"
	"github.com/fil-forge/go-ucanto/core/result/ok"
	"github.com/fil-forge/go-ucanto/core/schema"
	"github.com/fil-forge/go-ucanto/validator"
	"github.com/ipld/go-ipld-prime/datamodel"

	"github.com/fil-forge/go-libstoracha/capabilities/types"
)

const RegisterAbility = "admin/provider/register"

type RegisterCaveats struct {
	Endpoint string
	Proof    ipld.Link
}

func (rc RegisterCaveats) ToIPLD() (datamodel.Node, error) {
	return ipld.WrapWithRecovery(&rc, RegisterCaveatsType(), types.Converters...)
}

type RegisterOk = ok.Unit

var Register = validator.NewCapability(
	RegisterAbility,
	schema.DIDString(),
	schema.Struct[RegisterCaveats](RegisterCaveatsType(), nil, types.Converters...),
	validator.DefaultDerives[RegisterCaveats],
)
