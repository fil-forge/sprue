package datamodel

import "github.com/fil-forge/ucantone/did"

type ListArgumentsModel struct{}

type ProviderModel struct {
	Provider          did.DID `cborgen:"provider" dagjsongen:"provider"`
	Endpoint          string  `cborgen:"endpoint" dagjsongen:"endpoint"`
	Weight            int64   `cborgen:"weight" dagjsongen:"weight"`
	ReplicationWeight int64   `cborgen:"replicationWeight" dagjsongen:"replicationWeight"`
}

type ListOKModel struct {
	Providers []ProviderModel `cborgen:"providers" dagjsongen:"providers"`
}
