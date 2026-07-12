package tplink

import (
	"encoding/binary"
	"fmt"
)

// firstKey is the initial rolling-XOR key used by the TP-Link Smart Plug
// protocol on TCP port 9999. Each ciphertext byte becomes the key for the
// next plaintext byte, so decryption of a partial frame is impossible — the
// whole ciphertext must be present before any byte can be recovered.
const firstKey byte = 0xAB

// Encode returns a framed TCP payload: a 4-byte big-endian length prefix
// followed by the rolling-XOR-obfuscated plaintext.
func Encode(plaintext []byte) []byte {
	out := make([]byte, 4+len(plaintext))
	binary.BigEndian.PutUint32(out[:4], uint32(len(plaintext)))
	key := firstKey
	for i, b := range plaintext {
		c := b ^ key
		out[4+i] = c
		key = c
	}
	return out
}

// Decode reverses the rolling-XOR obfuscation over a ciphertext buffer that
// does NOT include the 4-byte length prefix. The caller is responsible for
// having accumulated exactly the declared number of ciphertext bytes before
// invoking Decode; per-segment decryption is not possible because the key
// stream is derived from the ciphertext itself.
func Decode(cipher []byte) ([]byte, error) {
	if len(cipher) == 0 {
		return []byte{}, nil
	}
	out := make([]byte, len(cipher))
	key := firstKey
	for i, c := range cipher {
		out[i] = c ^ key
		key = c
	}
	return out, nil
}

// decodeFrame is a convenience that validates a full frame (length prefix +
// ciphertext) and returns the plaintext.
func decodeFrame(frame []byte) ([]byte, error) {
	if len(frame) < 4 {
		return nil, fmt.Errorf("tplink: frame too short (%d bytes)", len(frame))
	}
	n := binary.BigEndian.Uint32(frame[:4])
	if uint32(len(frame)-4) < n {
		return nil, fmt.Errorf("tplink: frame length mismatch: header=%d body=%d", n, len(frame)-4)
	}
	return Decode(frame[4 : 4+n])
}
