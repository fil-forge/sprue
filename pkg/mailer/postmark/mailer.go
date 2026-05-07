package postmark

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"github.com/fil-forge/sprue/pkg/mailer"
)

const (
	defaultSender = "Storacha <support@storacha.network>"
	template      = "welcome-storacha"
)

var defaultEndpoint, _ = url.Parse("https://api.postmarkapp.com/email/withTemplate")

type Option func(*Mailer)

func WithHTTPClient(client *http.Client) Option {
	return func(cfg *Mailer) {
		cfg.client = client
	}
}

// WithEndpoint configures the API endpoint to send the email request to.
func WithEndpoint(endpoint url.URL) Option {
	return func(cfg *Mailer) {
		cfg.endpoint = endpoint
	}
}

// WithSender sets the sender email address. e.g. "Display Name <email@address.com>"
func WithSender(sender string) Option {
	return func(cfg *Mailer) {
		if sender != "" {
			cfg.sender = sender
		}
	}
}

// WithEnvironment sets the name to display in the email subject. Omit to show
// none i.e. production.
func WithEnvironment(env string) Option {
	return func(cfg *Mailer) {
		cfg.env = env
	}
}

type Mailer struct {
	client   *http.Client
	endpoint url.URL
	token    string
	sender   string
	env      string
}

var _ mailer.Mailer = (*Mailer)(nil)

func New(token string, opts ...Option) *Mailer {
	cfg := &Mailer{
		token:    token,
		sender:   defaultSender,
		endpoint: *defaultEndpoint,
		client:   http.DefaultClient,
	}
	for _, opt := range opts {
		opt(cfg)
	}
	return &Mailer{
		token:    token,
		sender:   cfg.sender,
		env:      cfg.env,
		endpoint: cfg.endpoint,
		client:   cfg.client,
	}
}

func (m *Mailer) SendValidation(ctx context.Context, to string, validationURL url.URL) error {
	body, err := json.Marshal(map[string]any{
		"From":          m.sender,
		"To":            to,
		"TemplateAlias": template,
		"TemplateModel": map[string]any{
			"email":            to,
			"action_url":       validationURL.String(),
			"environment_name": m.env,
		},
	})
	if err != nil {
		return fmt.Errorf("marshaling email parameters: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.endpoint.String(), bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("creating email request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Postmark-Server-Token", m.token)

	res, err := m.client.Do(req)
	if err != nil {
		return fmt.Errorf("sending email request: %w", err)
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(res.Body)
		return fmt.Errorf("email request failed with status: %s, body: %s", res.Status, string(body))
	}

	return nil
}
