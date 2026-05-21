package provider

import "github.com/fil-forge/ucantone/did"

type RegisterArguments struct {
	Provider did.DID `cborgen:"provider" dagjsongen:"provider"`
	Endpoint string  `cborgen:"endpoint" dagjsongen:"endpoint"`
}

type Provider struct {
	Provider          did.DID `cborgen:"provider" dagjsongen:"provider"`
	Endpoint          string  `cborgen:"endpoint" dagjsongen:"endpoint"`
	Weight            int64   `cborgen:"weight" dagjsongen:"weight"`
	ReplicationWeight int64   `cborgen:"replicationWeight" dagjsongen:"replicationWeight"`
}

type ListOK struct {
	Providers []Provider `cborgen:"providers" dagjsongen:"providers"`
}

type DeregisterArguments struct {
	Provider did.DID `cborgen:"provider" dagjsongen:"provider"`
}
