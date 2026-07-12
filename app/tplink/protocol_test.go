package tplink

import (
	"bytes"
	"encoding/hex"
	"testing"
)

func TestEncodeLengthPrefix(t *testing.T) {
	payload := []byte(`{"system":{"get_sysinfo":{}}}`)
	frame := Encode(payload)
	if len(frame) != 4+len(payload) {
		t.Fatalf("frame length: got %d, want %d", len(frame), 4+len(payload))
	}
	got := uint32(frame[0])<<24 | uint32(frame[1])<<16 | uint32(frame[2])<<8 | uint32(frame[3])
	if got != uint32(len(payload)) {
		t.Fatalf("length prefix: got %d, want %d", got, len(payload))
	}
}

func TestRoundTrip(t *testing.T) {
	cases := [][]byte{
		{},
		[]byte("a"),
		[]byte(`{"system":{"get_sysinfo":{}}}`),
		[]byte(`{"system":{"get_sysinfo":{}},"emeter":{"get_realtime":{}}}`),
		bytes.Repeat([]byte{0x00}, 512),
		bytes.Repeat([]byte{0xFF}, 512),
	}
	for _, in := range cases {
		frame := Encode(in)
		out, err := Decode(frame[4:])
		if err != nil {
			t.Fatalf("Decode(%q): %v", in, err)
		}
		if !bytes.Equal(in, out) {
			t.Fatalf("round trip mismatch: got %q, want %q", out, in)
		}
	}
}

func TestKnownCiphertext(t *testing.T) {
	plaintext := []byte(`{"system":{"get_sysinfo":{}}}`)
	// Known ciphertext (post-length-prefix) captured from the reference
	// NodeJS implementation for the same plaintext.
	// Cross-checked byte-for-byte with the spec's rolling-XOR definition
	// (initial key 0xAB, each ciphertext byte becomes the next key).
	wantHex := "d0f281f88bff9af7d5ef94b6d1b4c09fec95e68fe187e8caf08bf68bf6"
	want, err := hex.DecodeString(wantHex)
	if err != nil {
		t.Fatalf("hex decode: %v", err)
	}
	frame := Encode(plaintext)
	got := frame[4:]
	if !bytes.Equal(got, want) {
		t.Fatalf("Encode:\n got  %x\n want %x", got, want)
	}
	back, err := Decode(want)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if !bytes.Equal(back, plaintext) {
		t.Fatalf("Decode: got %q, want %q", back, plaintext)
	}
}

func TestDecodeFrameShort(t *testing.T) {
	if _, err := decodeFrame([]byte{0x00, 0x01}); err == nil {
		t.Fatal("expected error for short frame")
	}
	// Length prefix says 10 bytes, only 2 supplied.
	buf := []byte{0x00, 0x00, 0x00, 0x0A, 0x01, 0x02}
	if _, err := decodeFrame(buf); err == nil {
		t.Fatal("expected error for length mismatch")
	}
}
