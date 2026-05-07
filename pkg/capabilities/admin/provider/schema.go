package provider

import (
	_ "embed"
	"fmt"

	"github.com/fil-forge/go-libstoracha/capabilities/types"
	"github.com/ipld/go-ipld-prime/schema"
)

//go:embed provider.ipldsch
var providerSchema []byte

var providerTS = mustLoadTS()

func mustLoadTS() *schema.TypeSystem {
	ts, err := types.LoadSchemaBytes(providerSchema)
	if err != nil {
		panic(fmt.Errorf("loading provider schema: %w", err))
	}
	return ts
}

func RegisterCaveatsType() schema.Type {
	return providerTS.TypeByName("RegisterCaveats")
}

func RegisterOkType() schema.Type {
	return providerTS.TypeByName("RegisterOk")
}

func DeregisterCaveatsType() schema.Type {
	return providerTS.TypeByName("DeregisterCaveats")
}

func DeregisterOkType() schema.Type {
	return providerTS.TypeByName("DeregisterOk")
}

func ListCaveatsType() schema.Type {
	return providerTS.TypeByName("ListCaveats")
}

func ListOkType() schema.Type {
	return providerTS.TypeByName("ListOk")
}

func WeightSetCaveatsType() schema.Type {
	return providerTS.TypeByName("WeightSetCaveats")
}

func WeightSetOkType() schema.Type {
	return providerTS.TypeByName("WeightSetOk")
}
