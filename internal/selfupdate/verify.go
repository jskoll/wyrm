package selfupdate

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"strings"
)

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
