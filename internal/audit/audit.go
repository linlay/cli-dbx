package audit

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"
)

func ID(input string) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s:%d", input, time.Now().UnixNano())))
	return hex.EncodeToString(sum[:8])
}

func Fingerprint(input string) string {
	sum := sha256.Sum256([]byte(input))
	return hex.EncodeToString(sum[:12])
}
