package cryptox

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
)

const formatVersion byte = 1

type Cipher struct {
	aead cipher.AEAD
}

func New(key []byte) (*Cipher, error) {
	if len(key) != 32 {
		return nil, errors.New("credential encryption key must be 32 bytes")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Cipher{aead: aead}, nil
}

func (c *Cipher) Encrypt(plaintext, associatedData []byte) ([]byte, error) {
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("generate encryption nonce: %w", err)
	}
	out := make([]byte, 1, 1+len(nonce)+len(plaintext)+c.aead.Overhead())
	out[0] = formatVersion
	out = append(out, nonce...)
	out = c.aead.Seal(out, nonce, plaintext, associatedData)
	return out, nil
}

func (c *Cipher) Decrypt(blob, associatedData []byte) ([]byte, error) {
	min := 1 + c.aead.NonceSize() + c.aead.Overhead()
	if len(blob) < min || blob[0] != formatVersion {
		return nil, errors.New("invalid encrypted credential format")
	}
	nonce := blob[1 : 1+c.aead.NonceSize()]
	ciphertext := blob[1+c.aead.NonceSize():]
	plaintext, err := c.aead.Open(nil, nonce, ciphertext, associatedData)
	if err != nil {
		return nil, errors.New("decrypt credentials: authentication failed")
	}
	return plaintext, nil
}
