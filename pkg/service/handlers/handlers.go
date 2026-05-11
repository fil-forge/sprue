package handlers

import (
	"github.com/fil-forge/ucantone/execution"
	"github.com/fil-forge/ucantone/validator"
)

type Handler struct {
	Capability validator.Capability
	Handler    execution.HandlerFunc
}
