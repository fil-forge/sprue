package didmailto

import (
	"fmt"
	"net/mail"
	"net/url"
	"strings"

	"github.com/fil-forge/go-ucanto/did"
	"github.com/fil-forge/sprue/pkg/lib/errors"
)

const InvalidMailtoDIDErrorName = "InvalidMailtoDID"

var ErrInvalidMailtoDID = errors.New(InvalidMailtoDIDErrorName, "invalid mailto DID")

// New creates a new mailto DID from an RFC 5322 formatted email address.
func New(email string) (did.DID, error) {
	a, err := mail.ParseAddress(email)
	if err != nil {
		return did.Undef, fmt.Errorf("parsing email: %w", err)
	}
	at := strings.LastIndex(a.Address, "@")
	var local, domain string
	if at < 0 {
		// This is a malformed address ("@" is required in addr-spec);
		return did.Undef, fmt.Errorf("malformed email address: %s", email)
	}
	local, domain = a.Address[:at], a.Address[at+1:]
	return did.Parse(fmt.Sprintf("did:mailto:%s:%s", url.QueryEscape(domain), url.QueryEscape(local)))
}

// Email extracts the email address from the DID.
func Email(d did.DID) (string, error) {
	if !strings.HasPrefix(d.String(), "did:mailto:") {
		return "", ErrInvalidMailtoDID
	}
	parts := strings.Split(d.String(), ":")
	emailLocal, err := url.QueryUnescape(parts[3])
	if err != nil {
		return "", ErrInvalidMailtoDID
	}
	domain, err := url.QueryUnescape(parts[2])
	if err != nil {
		return "", ErrInvalidMailtoDID
	}
	return fmt.Sprintf("%s@%s", emailLocal, domain), nil
}

func Parse(str string) (did.DID, error) {
	d, err := did.Parse(str)
	if err != nil {
		return did.Undef, err
	}
	if !strings.HasPrefix(d.String(), "did:mailto:") {
		return did.Undef, ErrInvalidMailtoDID
	}
	return d, nil
}

func Format(d did.DID) string {
	return d.String()
}
