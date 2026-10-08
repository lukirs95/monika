// Package secret verwaltet Zugangsdaten: benannte Secrets, verschlüsselt mit AES-256-GCM.
//
// Ver- und entschlüsselt wird nur in Go; die Datenbank sieht nie einen Klartext oder Schlüssel.
// Werte werden nie angezeigt, geloggt oder ins Audit geschrieben.
package secret

import (
	"time"

	"github.com/google/uuid"
)

// Secret beschreibt ein Secret ohne seinen Wert, z. B. für die Auswahl in der UI.
type Secret struct {
	ID          uuid.UUID
	Name        string
	Description string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// Encrypted ist der verschlüsselte Wert eines Secrets.
type Encrypted struct {
	ID         uuid.UUID // Associated Data: bindet den Wert an genau dieses Secret
	Ciphertext []byte
	Nonce      []byte
	KeyID      string // Master-Key, mit dem verschlüsselt wurde
}
