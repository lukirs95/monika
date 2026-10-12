// Package http ist der Treiber für HTTP-Endpunkte als Sende-Ziel: Jeder konfigurierte Aufruf ist
// ein Element mit der Aktion trigger (drivers/http/README.md, docs/ARCHITECTURE.md Abschnitt 11).
package http

import (
	"encoding/json"
	"time"

	"github.com/lukirs95/monika/sdk"
	"github.com/lukirs95/monika/sdk/vocab"
)

// Name ist der Name des Treibers (device_source.driver).
const Name = "http"

const factStatusCode = "http.status_code"

func init() { sdk.Register(driver) }

var driver = sdk.Driver{
	Name:         Name,
	ConfigSchema: json.RawMessage(configSchema),
	Actions:      []sdk.ActionDef{{Name: vocab.ActionTrigger, Timeout: 30 * time.Second}},
	Facts: []sdk.FactDef{{Key: factStatusCode, Type: vocab.Int, Kind: vocab.Status,
		Description: "HTTP-Status der letzten Antwort; 0, wenn keine kam"}},
	New: newSource,
}
