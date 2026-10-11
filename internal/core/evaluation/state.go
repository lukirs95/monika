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
	Unreachable(sourceID uuid.UUID) bool
	Heartbeat(sourceID uuid.UUID) time.Duration
}
