package smtp

import (
	"context"
	"fmt"
	"net/smtp"
	"net/url"

	"github.com/fil-forge/sprue/pkg/mailer"
)

const (
	defaultSender  = "Storacha <support@storacha.network>"
	defaultSubject = "Welcome to Storacha!"
)

type Option func(*Mailer)

// WithSender sets the sender email address. e.g. "Display Name <email@address.com>"
func WithSender(sender string) Option {
	return func(cfg *Mailer) {
		if sender != "" {
			cfg.sender = sender
		}
	}
}

// WithSubject sets the email subject.
func WithSubject(subject string) Option {
	return func(cfg *Mailer) {
		if subject != "" {
			cfg.subject = subject
		}
	}
}

type Mailer struct {
	addr    string // e.g. mail.example.com:25
	sender  string // e.g. "Display Name <email@address.com>"
	subject string
	auth    smtp.Auth
}

var _ mailer.Mailer = (*Mailer)(nil)

func New(addr string, auth smtp.Auth, opts ...Option) *Mailer {
	m := Mailer{
		addr:    addr,
		auth:    auth,
		sender:  defaultSender,
		subject: defaultSubject,
	}
	for _, opt := range opts {
		opt(&m)
	}
	return &m
}

func (m *Mailer) SendValidation(ctx context.Context, to string, validationURL url.URL) error {
	msg := []byte(fmt.Sprintf("To: %s\r\n"+
		"Subject: %s\r\n"+
		"\r\n"+
		"%s\r\n", to, m.subject, validationURL.String()))
	err := smtp.SendMail(m.addr, m.auth, m.sender, []string{to}, msg)
	if err != nil {
		return fmt.Errorf("sending SMTP mail: %w", err)
	}
	return nil
}
