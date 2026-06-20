package unsubscribe

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strings"
)

var ErrInvalidToken = errors.New("invalid or tampered unsubscribe token")

// GenerateToken creates a signed token encoding userID and category.
// The token is safe to include in URLs (base64url, no padding).
// secret is the raw HMAC key (e.g. decoded TOTP encryption key).
func GenerateToken(userID, category string, secret []byte) string {
	payload := base64.RawURLEncoding.EncodeToString([]byte(userID + ":" + category))
	mac := computeMAC(payload, secret)
	return payload + "." + mac
}

// VerifyToken parses and verifies a token, returning userID and category.
func VerifyToken(token string, secret []byte) (userID, category string, err error) {
	parts := strings.SplitN(token, ".", 2)
	if len(parts) != 2 {
		return "", "", ErrInvalidToken
	}
	payload, mac := parts[0], parts[1]

	expected := computeMAC(payload, secret)
	if !hmac.Equal([]byte(mac), []byte(expected)) {
		return "", "", ErrInvalidToken
	}

	decoded, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		return "", "", ErrInvalidToken
	}

	idx := strings.Index(string(decoded), ":")
	if idx < 1 {
		return "", "", ErrInvalidToken
	}
	return string(decoded[:idx]), string(decoded[idx+1:]), nil
}

func computeMAC(payload string, secret []byte) string {
	h := hmac.New(sha256.New, secret)
	h.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString(h.Sum(nil))
}
