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

// loadKey lee SECRET_KEY del entorno y la decodifica.
// Espera 32 bytes en base64 (AES-256). Falla rápido si está mal configurada.
func loadKey() error {
	raw := os.Getenv("SECRET_KEY")
	if raw == "" {
		return errors.New("SECRET_KEY no definida en entorno")
	}
	key, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return fmt.Errorf("SECRET_KEY inválida (no es base64): %w", err)
	}
	if len(key) != 32 {
		return fmt.Errorf("SECRET_KEY debe ser 32 bytes (AES-256), recibidos: %d", len(key))
	}
	encryptionKey = key
	return nil
}

func encrypt(plain string) (string, error) {
	if plain == "" {
		return "", nil
	}
	block, err := aes.NewCipher(encryptionKey)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	ct := gcm.Seal(nonce, nonce, []byte(plain), nil)
	return base64.StdEncoding.EncodeToString(ct), nil
}

func decrypt(b64 string) (string, error) {
	if b64 == "" {
		return "", nil
	}
	data, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return "", fmt.Errorf("payload no es base64: %w", err)
	}
	block, err := aes.NewCipher(encryptionKey)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	ns := gcm.NonceSize()
	if len(data) < ns {
		return "", errors.New("payload cifrado corrupto")
	}
	nonce, ct := data[:ns], data[ns:]
	plain, err := gcm.Open(nil, nonce, ct, nil)
	if err != nil {
		return "", fmt.Errorf("descifrado falló (¿cambió SECRET_KEY?): %w", err)
	}
	return string(plain), nil
}
