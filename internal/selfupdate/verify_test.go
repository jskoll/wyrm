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
	"strings"
	"testing"
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

func TestVerifyChecksumsSignatureValid(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	data := []byte("sha256sum  wyrm_1.0.0_linux_amd64.tar.gz\n")
	sig := ed25519.Sign(priv, data)

	// Test raw binary signature
	if err := VerifyChecksumsSignature(data, sig, pub); err != nil {
		t.Fatalf("VerifyChecksumsSignature raw: %v", err)
	}

	// Test hex encoded signature
	hexSig := []byte(hex.EncodeToString(sig))
	if err := VerifyChecksumsSignature(data, hexSig, pub); err != nil {
		t.Fatalf("VerifyChecksumsSignature hex: %v", err)
	}

	// Test base64 encoded signature
	b64Sig := []byte(base64.StdEncoding.EncodeToString(sig))
	if err := VerifyChecksumsSignature(data, b64Sig, pub); err != nil {
		t.Fatalf("VerifyChecksumsSignature base64: %v", err)
	}

	// Test Minisign format signature
	minisignContent := fmt.Sprintf("untrusted comment: signature from minisign secret key\n%s\ntrusted comment: timestamp:12345\n%s\n",
		base64.StdEncoding.EncodeToString(append([]byte{0x45, 0x64, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08}, sig...)),
		base64.StdEncoding.EncodeToString(sig))
	if err := VerifyChecksumsSignature(data, []byte(minisignContent), pub); err != nil {
		t.Fatalf("VerifyChecksumsSignature minisign: %v", err)
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
	if err := VerifyChecksumsSignature(tamperedData, sig, pub); err == nil {
		t.Fatal("VerifyChecksumsSignature: want error for tampered data, got nil")
	}

	// Wrong public key
	wrongPub, _, _ := ed25519.GenerateKey(rand.Reader)
	if err := VerifyChecksumsSignature(data, sig, wrongPub); err == nil {
		t.Fatal("VerifyChecksumsSignature: want error for wrong public key, got nil")
	}

	// Nil or empty public key
	if err := VerifyChecksumsSignature(data, sig, nil); err == nil {
		t.Fatal("VerifyChecksumsSignature: want error for nil public key, got nil")
	}
}
