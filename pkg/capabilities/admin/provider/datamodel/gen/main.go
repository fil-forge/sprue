package main

import (
	jsg "github.com/alanshaw/dag-json-gen"
	pdm "github.com/fil-forge/sprue/pkg/capabilities/admin/provider/datamodel"
	cbg "github.com/whyrusleeping/cbor-gen"
)

func main() {
	models := []any{
		pdm.ListArgumentsModel{},
		pdm.ProviderModel{},
		pdm.ListOKModel{},
		pdm.RegisterArgumentsModel{},
		pdm.DeregisterArgumentsModel{},
	}
	if err := cbg.WriteMapEncodersToFile("../cbor_gen.go", "datamodel", models...); err != nil {
		panic(err)
	}
	if err := jsg.WriteMapEncodersToFile("../json_gen.go", "datamodel", models...); err != nil {
		panic(err)
	}
}
