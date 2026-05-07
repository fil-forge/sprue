package didmailto

import (
	"testing"

	"github.com/fil-forge/go-ucanto/did"
	"github.com/stretchr/testify/require"
)

func TestNew(t *testing.T) {
	t.Run("simple email", func(t *testing.T) {
		d, err := New("user@example.com")
		require.NoError(t, err)
		require.Equal(t, "did:mailto:example.com:user", d.String())
	})

	t.Run("subdomain", func(t *testing.T) {
		d, err := New("user@mail.example.com")
		require.NoError(t, err)
		require.Equal(t, "did:mailto:mail.example.com:user", d.String())
	})

	t.Run("missing @ returns error", func(t *testing.T) {
		_, err := New("notanemail")
		require.Error(t, err)
	})

	t.Run("multiple @ returns error", func(t *testing.T) {
		_, err := New("a@b@c")
		require.Error(t, err)
	})

	t.Run("empty string returns error", func(t *testing.T) {
		_, err := New("")
		require.Error(t, err)
	})
}

func TestEmail(t *testing.T) {
	t.Run("round-trips simple email", func(t *testing.T) {
		d, err := New("user@example.com")
		require.NoError(t, err)
		email, err := Email(d)
		require.NoError(t, err)
		require.Equal(t, "user@example.com", email)
	})

	t.Run("round-trips local part with plus sign", func(t *testing.T) {
		d, err := New("user+tag@example.com")
		require.NoError(t, err)
		email, err := Email(d)
		require.NoError(t, err)
		require.Equal(t, "user+tag@example.com", email)
	})

	t.Run("non-mailto DID returns error", func(t *testing.T) {
		d, err := did.Parse("did:web:example.com")
		require.NoError(t, err)
		_, err = Email(d)
		require.ErrorIs(t, err, ErrInvalidMailtoDID)
	})

	t.Run("round-trips unusual emails", func(t *testing.T) {
		emails := []string{
			// https://gist.github.com/cjaoude/fd9910626629b53c4d25#file-gistfile1-txt-L5
			"email@example.com",
			"firstname.lastname@example.com",
			"email@subdomain.example.com",
			"firstname+lastname@example.com",
			"email@123.123.123.123",
			"email@[123.123.123.123]",
			// TODO: quoted addresses do not roundtrip with net/mail because the
			// quotes are stripped out during parsing
			// "\"email\"@example.com",
			// "\"email@1\"@example.com",
			"1234567890@example.com",
			"email@example-one.com",
			"_______@example.com",
			"email@example.name",
			"email@example.museum",
			"email@example.co.jp",
			"firstname-lastname@example.com",
			// https://gist.github.com/cjaoude/fd9910626629b53c4d25#file-gistfile1-txt-L24
			// TODO: none of these parse with net/mail
			// "much.”more\\ unusual”@example.com",
			// "very.unusual.”@”.unusual.com@example.com",
			// "very.”(),:;<>[]”.VERY.”very@\\ \"very”.unusual@strange.example.com",
		}
		for _, email := range emails {
			t.Run(email, func(t *testing.T) {
				d, err := New(email)
				require.NoError(t, err)
				t.Log(d.String())
				got, err := Email(d)
				require.NoError(t, err)
				require.Equal(t, email, got)
			})
		}
	})
}

func TestParse(t *testing.T) {
	t.Run("valid mailto DID", func(t *testing.T) {
		d, err := Parse("did:mailto:example.com:user")
		require.NoError(t, err)
		require.Equal(t, "did:mailto:example.com:user", d.String())
	})

	t.Run("non-mailto DID returns error", func(t *testing.T) {
		_, err := Parse("did:web:example.com")
		require.ErrorIs(t, err, ErrInvalidMailtoDID)
	})

	t.Run("invalid DID string returns error", func(t *testing.T) {
		_, err := Parse("not-a-did")
		require.Error(t, err)
	})
}

func TestFormat(t *testing.T) {
	d, err := New("user@example.com")
	require.NoError(t, err)
	require.Equal(t, d.String(), Format(d))
}

func TestNewEmailRoundTrip(t *testing.T) {
	emails := []string{
		"user@example.com",
		"user+tag@example.com",
		"first.last@sub.domain.org",
	}
	for _, email := range emails {
		t.Run(email, func(t *testing.T) {
			d, err := New(email)
			require.NoError(t, err)
			got, err := Email(d)
			require.NoError(t, err)
			require.Equal(t, email, got)
		})
	}
}
