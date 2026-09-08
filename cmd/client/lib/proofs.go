package lib

import (
	"os"

	"github.com/fil-forge/ucantone/ucan/container"
)

// DecodeProofs decodes a UCAN container from arg, which is either the encoded
// container itself or a path to a file containing it. The inline form is tried
// first so a file that happens to share the name of a valid container string
// does not shadow it.
func DecodeProofs(arg string) (*container.Container, error) {
	if ct, err := container.Decode([]byte(arg)); err == nil {
		return ct, nil
	}
	data, err := os.ReadFile(arg)
	if err != nil {
		return nil, err
	}
	return container.Decode(data)
}
