package storage

import (
	"crypto/rand"
	"testing"
)

func setupKey(t *testing.T) {
	t.Helper()
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	encryptionKey = key
}

func TestEncryptDecrypt_Roundtrip(t *testing.T) {
	setupKey(t)
	cases := []string{"hola", "p@ssw0rd!#$", "ñandú", ""}
	for _, in := range cases {
		ct, err := encrypt(in)
		if err != nil {
			t.Fatalf("encrypt(%q): %v", in, err)
		}
		got, err := decrypt(ct)
		if err != nil {
			t.Fatalf("decrypt: %v", err)
		}
		if got != in {
			t.Errorf("roundtrip falló: in=%q out=%q", in, got)
		}
	}
}

func TestEncrypt_NonceIsRandom(t *testing.T) {
	setupKey(t)
	a, _ := encrypt("mismo-texto")
	b, _ := encrypt("mismo-texto")
	if a == b {
		t.Error("dos cifrados del mismo plaintext NO deben coincidir (nonce aleatorio)")
	}
}

func TestDecrypt_RejectsTamperedCiphertext(t *testing.T) {
	setupKey(t)
	ct, _ := encrypt("secreto")
	// Corromper último byte (cambia tag GCM o ciphertext).
	bad := ct[:len(ct)-2] + "AA"
	if _, err := decrypt(bad); err == nil {
		t.Error("decrypt debe rechazar payload alterado")
	}
}
