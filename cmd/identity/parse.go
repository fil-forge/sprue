package identity

import (
	crypto_ed25519 "crypto/ed25519"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"io"
	"os"

	signer "github.com/fil-forge/ucantone/principal/ed25519"
	verifier "github.com/fil-forge/ucantone/principal/ed25519/verifier"
	"github.com/spf13/cobra"
)

var parseCmd = &cobra.Command{
	Use:   "parse <path>",
	Short: "Parse a Ed25519 key from a PEM encoded file.",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		pemFile, err := os.Open(args[0])
		if err != nil {
			return fmt.Errorf("opening pem file: %w", err)
		}

		pemData, err := io.ReadAll(pemFile)
		if err != nil {
			return fmt.Errorf("reading pem file: %w", err)
		}

		blk, _ := pem.Decode(pemData)
		if blk == nil {
			return fmt.Errorf("no PEM block found")
		}

		switch blk.Type {
		case "PUBLIC KEY":
			pk, err := x509.ParsePKIXPublicKey(blk.Bytes)
			if err != nil {
				return fmt.Errorf("parsing PKIX public key: %w", err)
			}

			ed25519PK, ok := pk.(crypto_ed25519.PublicKey)
			if !ok {
				return fmt.Errorf("PKIX public key does not implement ed25519")
			}

			key, err := verifier.FromRaw(ed25519PK)
			if err != nil {
				return fmt.Errorf("decoding ed25519 public key: %w", err)
			}
			fmt.Printf("%s\n", key.DID())
		case "PRIVATE KEY":
			sk, err := x509.ParsePKCS8PrivateKey(blk.Bytes)
			if err != nil {
				return fmt.Errorf("parsing PKCS#8 private key: %w", err)
			}

			ed25519SK, ok := sk.(crypto_ed25519.PrivateKey)
			if !ok {
				return fmt.Errorf("PKCS#8 private key does not implement ed25519")
			}

			key, err := signer.FromRaw(ed25519SK.Seed())
			if err != nil {
				return fmt.Errorf("decoding ed25519 private key: %w", err)
			}
			fmt.Printf("%s\n", key.DID())
		default:
			return fmt.Errorf("unsupported PEM block type: %s", blk.Type)
		}
		return nil
	},
}
