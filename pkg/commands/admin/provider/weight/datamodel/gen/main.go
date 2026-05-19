package main

import (
	jsg "github.com/alanshaw/dag-json-gen"
	wdm "github.com/fil-forge/sprue/pkg/commands/admin/provider/weight/datamodel"
	cbg "github.com/whyrusleeping/cbor-gen"
)

func main() {
	models := []any{
		wdm.SetArgumentsModel{},
	}
	if err := cbg.WriteMapEncodersToFile("../cbor_gen.go", "datamodel", models...); err != nil {
		panic(err)
	}
	if err := jsg.WriteMapEncodersToFile("../json_gen.go", "datamodel", models...); err != nil {
		panic(err)
	}
}
