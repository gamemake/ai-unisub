package cryptox

import (
	"bytes"
	"testing"
)

func TestCipherRoundTripAndTamperDetection(t *testing.T) {
	key := bytes.Repeat([]byte{0x42}, 32)
	cipher, err := New(key)
	if err != nil {
		t.Fatal(err)
	}
	plaintext := []byte(`{"access_token":"secret"}`)
	sealed, err := cipher.Encrypt(plaintext, []byte("local-v1"))
	if err != nil {
		t.Fatal(err)
	}
	opened, err := cipher.Decrypt(sealed, []byte("local-v1"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(opened, plaintext) {
		t.Fatalf("round trip mismatch: %q", opened)
	}
	sealed[len(sealed)-1] ^= 1
	if _, err := cipher.Decrypt(sealed, []byte("local-v1")); err == nil {
		t.Fatal("tampered ciphertext was accepted")
	}
}
