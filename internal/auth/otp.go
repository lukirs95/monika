package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1" //nolint:gosec // RFC 6238 mit SHA-1, wie alle gängigen Authenticator-Apps (M3)
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"net/url"
	"time"
)

// TOTP nach RFC 6238 (M3).
const (
	totpPeriod = 30 * time.Second
	totpDigits = 6
	totpSkew   = 1 // Zeitschritte Toleranz in beide Richtungen
	totpIssuer = "Monika"
)

var totpEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)

// newTOTPSecret liefert 20 zufällige Bytes.
func newTOTPSecret() []byte {
	b := make([]byte, 20)
	_, _ = rand.Read(b)
	return b
}

// enrollment liefert die otpauth-URL für den QR-Code und das Geheimnis zum Abtippen.
func enrollment(secret []byte, username string) Enrollment {
	s := totpEncoding.EncodeToString(secret)
	q := url.Values{"secret": {s}, "issuer": {totpIssuer}, "algorithm": {"SHA1"}, "digits": {"6"}, "period": {"30"}}
	u := url.URL{Scheme: "otpauth", Host: "totp", Path: "/" + totpIssuer + ":" + username, RawQuery: q.Encode()}
	return Enrollment{OTPAuthURL: u.String(), Secret: s}
}

// Enrollment ist die Einrichtung von TOTP: URL für den QR-Code und Geheimnis zum Abtippen.
type Enrollment struct {
	OTPAuthURL string
	Secret     string
}

// totpCode berechnet den Code eines Zeitschritts (RFC 4226, dynamische Kürzung).
func totpCode(secret []byte, step int64) string {
	mac := hmac.New(sha1.New, secret)
	_ = binary.Write(mac, binary.BigEndian, step)
	sum := mac.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	v := binary.BigEndian.Uint32(sum[off:off+4]) & 0x7fffffff
	return fmt.Sprintf("%0*d", totpDigits, v%1_000_000)
}

// checkTOTP prüft einen Code mit ±1 Zeitschritt Toleranz; gültig ist nur ein Zeitschritt nach
// lastStep, damit ein Code nur einmal gilt. Er liefert den verwendeten Zeitschritt.
func checkTOTP(secret []byte, code string, now time.Time, lastStep int64) (int64, bool) {
	current := now.Unix() / int64(totpPeriod/time.Second)
	for d := -totpSkew; d <= totpSkew; d++ {
		step := current + int64(d)
		if step <= lastStep {
			continue
		}
		if subtle.ConstantTimeCompare([]byte(totpCode(secret, step)), []byte(code)) == 1 {
			return step, true
		}
	}
	return 0, false
}
