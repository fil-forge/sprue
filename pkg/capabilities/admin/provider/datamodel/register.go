package datamodel

import "github.com/fil-forge/ucantone/did"

type RegisterArgumentsModel struct {
	Provider did.DID `cborgen:"provider" dagjsongen:"provider"`
	Endpoint string  `cborgen:"endpoint" dagjsongen:"endpoint"`
}
