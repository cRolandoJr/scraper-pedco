package storage

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
)

var encryptionKey []byte

// loadEncryptionKey lee SECRET_KEY del entorno y la decodifica.
// Espera 32 bytes en base64 (AES-256). Falla rápido si está mal configurada.
func loadEncryptionKey() error {
	rawKey := os.Getenv("SECRET_KEY")
	if rawKey == "" {
		return errors.New("SECRET_KEY no definida en entorno")
	}
	decodedKey, err := base64.StdEncoding.DecodeString(rawKey)
	if err != nil {
		return fmt.Errorf("SECRET_KEY inválida (no es base64): %w", err)
	}
	if len(decodedKey) != 32 {
		return fmt.Errorf("SECRET_KEY debe ser 32 bytes (AES-256), recibidos: %d", len(decodedKey))
	}
	encryptionKey = decodedKey
	return nil
}

func encrypt(plaintext string) (string, error) {
	if plaintext == "" {
		return "", nil
	}
	cipherBlock, err := aes.NewCipher(encryptionKey)
	if err != nil {
		return "", err
	}
	gcmCipher, err := cipher.NewGCM(cipherBlock)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcmCipher.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	ciphertext := gcmCipher.Seal(nonce, nonce, []byte(plaintext), nil)
	return base64.StdEncoding.EncodeToString(ciphertext), nil
}

func decrypt(encodedCiphertext string) (string, error) {
	if encodedCiphertext == "" {
		return "", nil
	}
	rawCiphertext, err := base64.StdEncoding.DecodeString(encodedCiphertext)
	if err != nil {
		return "", fmt.Errorf("payload no es base64: %w", err)
	}
	cipherBlock, err := aes.NewCipher(encryptionKey)
	if err != nil {
		return "", err
	}
	gcmCipher, err := cipher.NewGCM(cipherBlock)
	if err != nil {
		return "", err
	}
	nonceSize := gcmCipher.NonceSize()
	if len(rawCiphertext) < nonceSize {
		return "", errors.New("payload cifrado corrupto")
	}
	nonce, ciphertext := rawCiphertext[:nonceSize], rawCiphertext[nonceSize:]
	plaintext, err := gcmCipher.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", fmt.Errorf("descifrado falló (¿cambió SECRET_KEY?): %w", err)
	}
	return string(plaintext), nil
}
