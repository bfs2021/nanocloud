package update

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"hash"
	"strings"
)

// Just enough OpenPGP (RFC 4880) to check the one kind of signature releases
// carry: a v4 detached signature by an Ed25519 key (public-key algorithm 22).
// A general OpenPGP library pulls in post-quantum suites and grows the binary
// by a fifth for this one check. Every field is parsed strictly and anything
// else is refused; the cryptography is crypto/ed25519's, over a digest
// computed here from the data being verified, so a parsing mistake can make a
// genuine signature fail but cannot make a forged one pass.

const (
	pktSignature = 2
	pktPublicKey = 6

	algoEdDSA  = 22
	hashSHA256 = 8
	hashSHA512 = 10
	sigBinary  = 0x00
)

// ed25519OID is the curve OID OpenPGP names Ed25519 keys by.
var ed25519OID = []byte{0x2b, 0x06, 0x01, 0x04, 0x01, 0xda, 0x47, 0x0f, 0x01}

// verifyDetached checks armoredSig is pub's signature over data.
func verifyDetached(pub ed25519.PublicKey, data, armoredSig []byte) error {
	raw, err := dearmor(armoredSig, "PGP SIGNATURE")
	if err != nil {
		return err
	}
	body, err := packet(raw, pktSignature)
	if err != nil {
		return err
	}
	if len(body) < 6 || body[0] != 4 {
		return errors.New("pgp: not a version 4 signature")
	}
	if body[1] != sigBinary || body[2] != algoEdDSA {
		return fmt.Errorf("pgp: signature class %#x, algorithm %d: want a binary EdDSA signature", body[1], body[2])
	}
	var h hash.Hash
	switch body[3] {
	case hashSHA256:
		h = sha256.New()
	case hashSHA512:
		h = sha512.New()
	default:
		return fmt.Errorf("pgp: unsupported hash algorithm %d", body[3])
	}
	hashedEnd := 6 + int(binary.BigEndian.Uint16(body[4:6]))
	if hashedEnd+2 > len(body) {
		return errors.New("pgp: truncated signature")
	}
	unhashedEnd := hashedEnd + 2 + int(binary.BigEndian.Uint16(body[hashedEnd:hashedEnd+2]))
	if unhashedEnd+2 > len(body) {
		return errors.New("pgp: truncated signature")
	}
	rest := body[unhashedEnd+2:] // past the digest's two-byte prefix
	r, rest, err := mpi(rest)
	if err != nil {
		return err
	}
	s, rest, err := mpi(rest)
	if err != nil || len(rest) != 0 || len(r) > 32 || len(s) > 32 {
		return errors.New("pgp: malformed EdDSA signature values")
	}

	// The signed message: the data, the hashed part of the packet, and a
	// trailer giving that part's length (RFC 4880 §5.2.4).
	h.Write(data)
	h.Write(body[:hashedEnd])
	var trailer [6]byte
	trailer[0], trailer[1] = 4, 0xff
	binary.BigEndian.PutUint32(trailer[2:], uint32(hashedEnd))
	h.Write(trailer[:])

	sig := make([]byte, 64)
	copy(sig[32-len(r):32], r)
	copy(sig[64-len(s):], s)
	if !ed25519.Verify(pub, h.Sum(nil), sig) {
		return errors.New("pgp: bad signature")
	}
	return nil
}

// parsePublicKey returns the Ed25519 key in an armored v4 public key block.
func parsePublicKey(armored []byte) (ed25519.PublicKey, error) {
	raw, err := dearmor(armored, "PGP PUBLIC KEY BLOCK")
	if err != nil {
		return nil, err
	}
	body, err := packet(raw, pktPublicKey)
	if err != nil {
		return nil, err
	}
	// version, four bytes of creation time, algorithm, OID length, OID.
	if len(body) < 7 || body[0] != 4 || body[5] != algoEdDSA {
		return nil, errors.New("pgp: not a version 4 EdDSA public key")
	}
	oidEnd := 7 + int(body[6])
	if oidEnd > len(body) || !bytes.Equal(body[7:oidEnd], ed25519OID) {
		return nil, errors.New("pgp: not an Ed25519 key")
	}
	point, _, err := mpi(body[oidEnd:])
	// The point is 0x40 followed by the 32-byte key.
	if err != nil || len(point) != 33 || point[0] != 0x40 {
		return nil, errors.New("pgp: malformed Ed25519 public key")
	}
	return ed25519.PublicKey(point[1:]), nil
}

// dearmor decodes the first ASCII-armored block of the given type. The
// checksum line is skipped: what the block holds is verified on its own.
func dearmor(armored []byte, blockType string) ([]byte, error) {
	text := strings.ReplaceAll(string(armored), "\r\n", "\n")
	begin := "-----BEGIN " + blockType + "-----"
	start := strings.Index(text, begin)
	if start < 0 {
		return nil, fmt.Errorf("pgp: no %s block", blockType)
	}
	end := strings.Index(text[start:], "-----END "+blockType+"-----")
	if end < 0 {
		return nil, fmt.Errorf("pgp: unterminated %s block", blockType)
	}
	var b64 strings.Builder
	for _, line := range strings.Split(text[start+len(begin):start+end], "\n") {
		line = strings.TrimSpace(line)
		// Header lines ("Key: value") and the checksum ("=XXXX") are not data.
		if line == "" || strings.Contains(line, ": ") || strings.HasPrefix(line, "=") {
			continue
		}
		b64.WriteString(line)
	}
	return base64.StdEncoding.DecodeString(b64.String())
}

// packet returns the body of the first packet in raw, which must have tag.
func packet(raw []byte, tag int) ([]byte, error) {
	if len(raw) < 2 || raw[0]&0x80 == 0 {
		return nil, errors.New("pgp: not an OpenPGP packet")
	}
	var got, n, hdr int
	if raw[0]&0x40 == 0 { // old format: tag in bits 5–2, length type in 1–0
		got = int(raw[0]>>2) & 0x0f
		switch raw[0] & 3 {
		case 0:
			n, hdr = int(raw[1]), 2
		case 1:
			if len(raw) < 3 {
				return nil, errors.New("pgp: truncated packet")
			}
			n, hdr = int(binary.BigEndian.Uint16(raw[1:3])), 3
		case 2:
			if len(raw) < 5 {
				return nil, errors.New("pgp: truncated packet")
			}
			n, hdr = int(binary.BigEndian.Uint32(raw[1:5])), 5
		default:
			return nil, errors.New("pgp: indeterminate packet length")
		}
	} else { // new format
		got = int(raw[0] & 0x3f)
		switch l := int(raw[1]); {
		case l < 192:
			n, hdr = l, 2
		case l < 224:
			if len(raw) < 3 {
				return nil, errors.New("pgp: truncated packet")
			}
			n, hdr = (l-192)<<8+int(raw[2])+192, 3
		case l == 255:
			if len(raw) < 6 {
				return nil, errors.New("pgp: truncated packet")
			}
			n, hdr = int(binary.BigEndian.Uint32(raw[2:6])), 6
		default:
			return nil, errors.New("pgp: partial-length packets are not supported")
		}
	}
	if got != tag {
		return nil, fmt.Errorf("pgp: packet tag %d, want %d", got, tag)
	}
	if n < 0 || hdr+n > len(raw) {
		return nil, errors.New("pgp: truncated packet")
	}
	return raw[hdr : hdr+n], nil
}

// mpi reads one multiprecision integer: a two-byte bit count, then the bytes.
func mpi(b []byte) (value, rest []byte, err error) {
	if len(b) < 2 {
		return nil, nil, errors.New("pgp: truncated integer")
	}
	n := (int(binary.BigEndian.Uint16(b)) + 7) / 8
	if len(b) < 2+n {
		return nil, nil, errors.New("pgp: truncated integer")
	}
	return b[2 : 2+n], b[2+n:], nil
}
