package provider

import (
	"github.com/fil-forge/go-ucanto/core/ipld"
	"github.com/fil-forge/go-ucanto/core/schema"
	"github.com/fil-forge/go-ucanto/did"
	"github.com/fil-forge/go-ucanto/validator"
	"github.com/ipld/go-ipld-prime/datamodel"

	"github.com/fil-forge/go-libstoracha/capabilities/types"
)

const ListAbility = "admin/provider/list"

type ListCaveats struct{}

func (lc ListCaveats) ToIPLD() (datamodel.Node, error) {
	return ipld.WrapWithRecovery(&lc, ListCaveatsType(), types.Converters...)
}

type Provider struct {
	ID                did.DID
	Endpoint          string
	Weight            int
	ReplicationWeight int
}

type ListOk struct {
	Providers []Provider
}

func (lo ListOk) ToIPLD() (datamodel.Node, error) {
	return ipld.WrapWithRecovery(&lo, ListOkType(), types.Converters...)
}

var List = validator.NewCapability(
	ListAbility,
	schema.DIDString(),
	schema.Struct[ListCaveats](ListCaveatsType(), nil, types.Converters...),
	validator.DefaultDerives[ListCaveats],
)
