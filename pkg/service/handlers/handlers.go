package handlers

import (
	"github.com/fil-forge/ucantone/execution"
	"github.com/fil-forge/ucantone/ucan"
)

type Handler struct {
	Command ucan.Command
	Handler execution.HandlerFunc
}
