package totp

import (
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"strings"
	"time"
)

// GenerateTOTP generates a standard 6-digit TOTP code for the given secret at the current time.
// It also returns the number of seconds remaining before the code expires.
func GenerateTOTP(secret string) (string, int, error) {
	// Clean up secret (remove spaces, convert to uppercase)
	secret = strings.ReplaceAll(secret, " ", "")
	secret = strings.ToUpper(secret)

	// Decode base32 secret
	// Standard TOTP secrets are base32 encoded. If it has no padding, we append padding character '='.
	if len(secret)%8 != 0 {
		secret += strings.Repeat("=", 8-(len(secret)%8))
	}

	key, err := base32.StdEncoding.DecodeString(secret)
	if err != nil {
		return "", 0, fmt.Errorf("invalid base32 secret: %v", err)
	}

	// Calculate counter based on current unix time (30-second steps)
	now := time.Now().Unix()
	step := int64(30)
	counter := now / step
	secondsRemaining := int(step - (now % step))

	// Convert counter to 8-byte big-endian byte array
	buf := make([]byte, 8)
	binary.BigEndian.PutUint64(buf, uint64(counter))

	// Calculate HMAC-SHA1
	mac := hmac.New(sha1.New, key)
	mac.Write(buf)
	sum := mac.Sum(nil)

	// Dynamic truncation
	offset := sum[len(sum)-1] & 0x0f
	binaryCode := (int32(sum[offset])&0x7f)<<24 |
		(int32(sum[offset+1])&0xff)<<16 |
		(int32(sum[offset+2])&0xff)<<8 |
		(int32(sum[offset+3])&0xff)

	otp := binaryCode % 1000000
	return fmt.Sprintf("%06d", otp), secondsRemaining, nil
}
