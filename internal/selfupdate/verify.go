package selfupdate

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"
)

// DefaultSigningPublicKey is the optional embedded Ed25519 public key used to verify releases.
var DefaultSigningPublicKey ed25519.PublicKey

// VerifyChecksumsSignature validates the cryptographic Ed25519 signature of checksumsTxt
// against the provided public key. It supports raw binary, hex, base64, and Minisign signatures.
func VerifyChecksumsSignature(checksumsTxt, signature []byte, pubKey ed25519.PublicKey) error {
	if len(pubKey) == 0 {
		return errors.New("no public key provided for signature verification")
	}
	if len(pubKey) != ed25519.PublicKeySize {
		return fmt.Errorf("invalid public key size: expected %d bytes, got %d", ed25519.PublicKeySize, len(pubKey))
	}
	rawSig, err := ParseSignature(signature)
	if err != nil {
		return fmt.Errorf("parsing signature: %w", err)
	}
	if !ed25519.Verify(pubKey, checksumsTxt, rawSig) {
		return errors.New("cryptographic signature verification failed for checksums.txt")
	}
	return nil
}

// ParseSignature extracts the 64-byte Ed25519 signature from raw, hex, base64, or minisign formats.
func ParseSignature(sig []byte) ([]byte, error) {
	trimmed := strings.TrimSpace(string(sig))

	// Minisign format:
	// untrusted comment: signature from minisign secret key
	// <base64>
	// trusted comment: ...
	// <base64>
	if strings.HasPrefix(trimmed, "untrusted comment:") {
		lines := strings.Split(trimmed, "\n")
		if len(lines) >= 2 {
			sigB64 := strings.TrimSpace(lines[1])
			decoded, err := base64.StdEncoding.DecodeString(sigB64)
			if err == nil && len(decoded) >= ed25519.SignatureSize {
				return decoded[len(decoded)-ed25519.SignatureSize:], nil
			}
		}
	}

	// Raw 64-byte signature
	if len(sig) == ed25519.SignatureSize {
		return sig, nil
	}

	// Hex-encoded 128 hex char signature
	if len(trimmed) == ed25519.SignatureSize*2 {
		if decoded, err := hex.DecodeString(trimmed); err == nil && len(decoded) == ed25519.SignatureSize {
			return decoded, nil
		}
	}

	// Base64-encoded signature
	if decoded, err := base64.StdEncoding.DecodeString(trimmed); err == nil {
		if len(decoded) == ed25519.SignatureSize {
			return decoded, nil
		}
		if len(decoded) > ed25519.SignatureSize {
			return decoded[len(decoded)-ed25519.SignatureSize:], nil
		}
	}

	return nil, errors.New("unsupported or invalid signature format")
}

// VerifyChecksum checks that data's SHA-256 matches the entry for filename
// in a checksums.txt file, in the "<hex>  <filename>" format both sha256sum
// and goreleaser's checksum block produce.
func VerifyChecksum(checksumsTxt []byte, filename string, data []byte) error {
	want, err := checksumFor(checksumsTxt, filename)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(data)
	got := hex.EncodeToString(sum[:])
	if got != want {
		return fmt.Errorf("checksum mismatch for %s: got %s, want %s", filename, got, want)
	}
	return nil
}

func checksumFor(checksumsTxt []byte, filename string) (string, error) {
	for _, line := range strings.Split(string(checksumsTxt), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[1] == filename {
			return fields[0], nil
		}
	}
	return "", fmt.Errorf("%s not listed in checksums.txt", filename)
}

// MaxBinarySize is the maximum permitted size (64 MB) for a single extracted binary.
const MaxBinarySize = 64 * 1024 * 1024

// ExtractFile pulls a single named regular file's contents out of a .tar.gz
// archive, bounded by MaxBinarySize to prevent decompression bombs and OOM crashes.
func ExtractFile(archive []byte, name string) ([]byte, error) {
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return nil, fmt.Errorf("opening archive: %w", err)
	}
	defer func() { _ = gz.Close() }()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("reading archive: %w", err)
		}
		if hdr.Typeflag == tar.TypeReg && hdr.Name == name {
			data, err := io.ReadAll(io.LimitReader(tr, MaxBinarySize+1))
			if err != nil {
				return nil, fmt.Errorf("reading %s from archive: %w", name, err)
			}
			if len(data) > MaxBinarySize {
				return nil, fmt.Errorf("%s exceeds maximum permitted binary size of %d bytes", name, MaxBinarySize)
			}
			return data, nil
		}
	}
	return nil, fmt.Errorf("%s not found in archive", name)
}
