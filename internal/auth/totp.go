package auth

import (
	"time"

	"github.com/lukirs95/monika/internal/secret"
)

// TOTP ist das zweite Merkmal eines interaktiven lokalen Benutzers. Das Geheimnis ist mit dem
// Master-Key verschlüsselt; Associated Data ist die ID des Benutzers.
type TOTP struct {
	Value       secret.Encrypted // Value.ID = User-ID
	ConfirmedAt time.Time        // Null-Zeit = Einrichtung noch nicht bestätigt
	LastStep    int64            // zuletzt verwendeter Zeitschritt; 0 = noch keiner
}
