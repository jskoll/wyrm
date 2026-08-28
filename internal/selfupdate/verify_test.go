package selfupdate

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/blake2b"
)

func buildArchive(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, content := range files {
		hdr := &tar.Header{Name: name, Mode: 0o755, Size: int64(len(content))}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestVerifyChecksumMatch(t *testing.T) {
	data := []byte("hello wyrm")
	sum := sha256.Sum256(data)
	checksums := []byte(fmt.Sprintf("%s  wyrm_1.0.0_linux_amd64.tar.gz\nsomeotherhash  someother.tar.gz\n", hex.EncodeToString(sum[:])))
	if err := VerifyChecksum(checksums, "wyrm_1.0.0_linux_amd64.tar.gz", data); err != nil {
		t.Fatalf("VerifyChecksum: %v", err)
	}
}

func TestVerifyChecksumMismatch(t *testing.T) {
	sum := sha256.Sum256([]byte("other data"))
	checksums := []byte(fmt.Sprintf("%s  wyrm_1.0.0_linux_amd64.tar.gz\n", hex.EncodeToString(sum[:])))
	if err := VerifyChecksum(checksums, "wyrm_1.0.0_linux_amd64.tar.gz", []byte("hello wyrm")); err == nil {
		t.Fatal("VerifyChecksum: want mismatch error, got nil")
	}
}

func TestVerifyChecksumMissingEntry(t *testing.T) {
	if err := VerifyChecksum([]byte("deadbeef  someother.tar.gz\n"), "wyrm_1.0.0_linux_amd64.tar.gz", []byte("x")); err == nil {
		t.Fatal("VerifyChecksum: want error for missing entry, got nil")
	}
}

func TestExtractFile(t *testing.T) {
	archive := buildArchive(t, map[string]string{
		"wyrm":          "binary-contents",
		"completions/x": "completion",
		"man/wyrm.1":    "man page",
	})
	data, err := ExtractFile(archive, "wyrm")
	if err != nil {
		t.Fatalf("ExtractFile: %v", err)
	}
	if string(data) != "binary-contents" {
		t.Errorf("ExtractFile = %q, want %q", data, "binary-contents")
	}
}

func TestExtractFileNotFound(t *testing.T) {
	archive := buildArchive(t, map[string]string{"README.md": "docs"})
	if _, err := ExtractFile(archive, "wyrm"); err == nil {
		t.Fatal("ExtractFile: want error for missing file, got nil")
	}
}

func TestExtractFileExceedsLimit(t *testing.T) {
	// Construct an archive with a header claiming or containing data > MaxBinarySize
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	oversize := MaxBinarySize + 10
	hdr := &tar.Header{Name: "wyrm", Mode: 0o755, Size: int64(oversize)}
	if err := tw.WriteHeader(hdr); err != nil {
		t.Fatal(err)
	}
	// Write MaxBinarySize + 10 bytes of zeroes
	chunk := make([]byte, 1024*1024)
	written := 0
	for written < oversize {
		toWrite := len(chunk)
		if oversize-written < toWrite {
			toWrite = oversize - written
		}
		if _, err := tw.Write(chunk[:toWrite]); err != nil {
			t.Fatal(err)
		}
		written += toWrite
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}

	_, err := ExtractFile(buf.Bytes(), "wyrm")
	if err == nil {
		t.Fatal("ExtractFile: want error for binary exceeding MaxBinarySize, got nil")
	}
	if !strings.Contains(err.Error(), "exceeds maximum permitted binary size") {
		t.Errorf("ExtractFile error = %q, want size limit message", err.Error())
	}
}

// minisignFile assembles a minisign .pub or .minisig payload: a comment line
// then base64 of algo | key ID | body.
func minisignFile(algo string, keyID [8]byte, body []byte) []byte {
	blob := append([]byte(algo), keyID[:]...)
	blob = append(blob, body...)
	return fmt.Appendf(nil, "untrusted comment: test\n%s\ntrusted comment: test\n%s\n",
		base64.StdEncoding.EncodeToString(blob),
		base64.StdEncoding.EncodeToString(body))
}

func TestVerifyChecksumsSignatureValid(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	data := []byte("sha256sum  wyrm_1.0.0_linux_amd64.tar.gz\n")
	sig := ed25519.Sign(priv, data)
	bare := SigningKeyFromEd25519(pub)

	t.Run("raw", func(t *testing.T) {
		if err := VerifyChecksumsSignature(data, sig, bare); err != nil {
			t.Fatalf("raw: %v", err)
		}
	})
	t.Run("hex", func(t *testing.T) {
		if err := VerifyChecksumsSignature(data, []byte(hex.EncodeToString(sig)), bare); err != nil {
			t.Fatalf("hex: %v", err)
		}
	})
	t.Run("base64", func(t *testing.T) {
		if err := VerifyChecksumsSignature(data, []byte(base64.StdEncoding.EncodeToString(sig)), bare); err != nil {
			t.Fatalf("base64: %v", err)
		}
	})

	keyID := [8]byte{1, 2, 3, 4, 5, 6, 7, 8}
	key, err := ParseMinisignPublicKey(minisignFile(algoLegacy, keyID, pub))
	if err != nil {
		t.Fatalf("ParseMinisignPublicKey: %v", err)
	}

	t.Run("minisign legacy Ed", func(t *testing.T) {
		if err := VerifyChecksumsSignature(data, minisignFile(algoLegacy, keyID, sig), key); err != nil {
			t.Fatalf("minisign Ed: %v", err)
		}
	})

	// What minisign 0.12 actually emits: the signature covers BLAKE2b-512 of
	// the file, not the file. Verifying it as though it were legacy "Ed" fails,
	// which is why a genuine minisign signature never validated here.
	t.Run("minisign prehashed ED", func(t *testing.T) {
		sum := blake2b.Sum512(data)
		preSig := ed25519.Sign(priv, sum[:])
		if err := VerifyChecksumsSignature(data, minisignFile(algoPrehash, keyID, preSig), key); err != nil {
			t.Fatalf("minisign ED: %v", err)
		}
		if err := VerifyChecksumsSignature(data, minisignFile(algoLegacy, keyID, preSig), key); err == nil {
			t.Fatal("a prehashed signature must not verify as legacy Ed")
		}
	})

	t.Run("key id mismatch is rejected", func(t *testing.T) {
		other := [8]byte{9, 9, 9, 9, 9, 9, 9, 9}
		if err := VerifyChecksumsSignature(data, minisignFile(algoLegacy, other, sig), key); err == nil {
			t.Fatal("want error for a signature made by a different key id")
		}
	})

	t.Run("unknown algorithm is rejected", func(t *testing.T) {
		if err := VerifyChecksumsSignature(data, minisignFile("Xx", keyID, sig), key); err == nil {
			t.Fatal("want error for an unknown minisign algorithm")
		}
	})
}

// A real release signing key is committed at signing.pub, and this insists it
// stays that way.
//
// It used to tolerate either state, because the file shipped as a placeholder
// and verification was disabled-but-loud. Now that releases are signed,
// reverting to a placeholder would silently turn `wyrm selfupdate` back into
// "checksums only" for every user on that build — a downgrade with no error
// anywhere. That is worth a failing test rather than a tolerated branch.
func TestEmbeddedSigningKeyIsReal(t *testing.T) {
	if !DefaultSigningKey.Valid() {
		t.Fatal("no usable key embedded: internal/selfupdate/signing.pub is missing or a placeholder, " +
			"which silently reduces selfupdate to checksum-only verification")
	}
	if len(DefaultSigningKey.key) != ed25519.PublicKeySize {
		t.Errorf("embedded key is marked valid but is %d bytes", len(DefaultSigningKey.key))
	}
	if !DefaultSigningKey.hasID {
		t.Error("embedded key should carry a minisign key id, so a signature naming another key is rejected")
	}
	if _, err := ParseMinisignPublicKey(signingPubFile); err != nil {
		t.Errorf("signing.pub does not parse: %v", err)
	}
}

func TestVerifyChecksumsSignatureInvalid(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	data := []byte("sha256sum  wyrm_1.0.0_linux_amd64.tar.gz\n")
	sig := ed25519.Sign(priv, data)

	tamperedData := []byte("tampered  wyrm_1.0.0_linux_amd64.tar.gz\n")
	if err := VerifyChecksumsSignature(tamperedData, sig, SigningKeyFromEd25519(pub)); err == nil {
		t.Fatal("VerifyChecksumsSignature: want error for tampered data, got nil")
	}

	// Wrong public key
	wrongPub, _, _ := ed25519.GenerateKey(rand.Reader)
	if err := VerifyChecksumsSignature(data, sig, SigningKeyFromEd25519(wrongPub)); err == nil {
		t.Fatal("VerifyChecksumsSignature: want error for wrong public key, got nil")
	}

	// Nil or empty public key
	if err := VerifyChecksumsSignature(data, sig, SigningKey{}); err == nil {
		t.Fatal("VerifyChecksumsSignature: want error for nil public key, got nil")
	}
}

// TestVerifyRealMinisignArtifacts checks the parser against files produced by
// the actual minisign binary (0.12), not by this test's own encoder.
//
// It exists because the hand-rolled fixture the old tests used was not a valid
// minisign file, and hid the fact that real minisign defaults to the prehashed
// "ED" algorithm — so a genuine signature would have failed verification even
// once a key was embedded. testdata/ holds a throwaway key pair's public half;
// the secret key was never saved.
func TestVerifyRealMinisignArtifacts(t *testing.T) {
	pubPEM, err := os.ReadFile(filepath.Join("testdata", "minisign.pub"))
	if err != nil {
		t.Fatal(err)
	}
	checksums, err := os.ReadFile(filepath.Join("testdata", "checksums.txt"))
	if err != nil {
		t.Fatal(err)
	}
	sig, err := os.ReadFile(filepath.Join("testdata", "checksums.txt.minisig"))
	if err != nil {
		t.Fatal(err)
	}

	key, err := ParseMinisignPublicKey(pubPEM)
	if err != nil {
		t.Fatalf("ParseMinisignPublicKey on a real minisign key: %v", err)
	}
	if !key.Valid() {
		t.Fatal("real minisign public key parsed but is not usable")
	}
	if err := VerifyChecksumsSignature(checksums, sig, key); err != nil {
		t.Fatalf("verifying a real minisign signature: %v", err)
	}

	// And it must actually be checking something.
	tampered := append([]byte("0"), checksums[1:]...)
	if err := VerifyChecksumsSignature(tampered, sig, key); err == nil {
		t.Fatal("tampered checksums.txt verified against a real signature")
	}
}
