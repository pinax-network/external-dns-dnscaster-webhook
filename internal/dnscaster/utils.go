package dnscaster

import (
	"crypto/rand"
	"encoding/hex"
)

// Generate a random hash of length 8
func genRandomHex() (string, error) {
	bytes := make([]byte, 4) // 2 hex chars per byte
	_, err := rand.Read(bytes)
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes), nil
}
