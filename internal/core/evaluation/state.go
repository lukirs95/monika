package evaluation

import (
	"time"

	"github.com/google/uuid"

	"github.com/lukirs95/monika/internal/core/inventory"
	"github.com/lukirs95/monika/sdk"
)

// State ist, was die Bewertung vom Zustand liest; die View des Core erfüllt es. Ein Interface
// beim Verwender, weil core dieses Paket importiert und nicht umgekehrt.
type State interface {
	Graph() *inventory.Graph
	Facts(elementID uuid.UUID) sdk.Facts
	Live(sourceID uuid.UUID) bool
	// Unreachable meldet, ob für die Quelle source.unreachable gilt, mit dem Grund für details.
	Unreachable(sourceID uuid.UUID) (reason string, ok bool)
	Heartbeat(sourceID uuid.UUID) time.Duration
}
