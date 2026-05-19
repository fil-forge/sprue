package datamodel

import "github.com/fil-forge/ucantone/did"

type DeregisterArgumentsModel struct {
	Provider did.DID `cborgen:"provider" dagjsongen:"provider"`
}
