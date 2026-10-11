// Package sdk ist die einzige Schnittstelle zwischen dem Core von Monika und den Treibern
// (docs/contracts/sdk.md). Treiber importieren nur sdk, sdk/vocab und ihre Gerätelibrary.
//
// Ein Treiber registriert sich in init() mit Register. Der Core erzeugt für jede Quelle
// (device_source) mit Driver.New eine eigene Source, startet ihr Run in einer eigenen Goroutine
// und hört über den Reporter zu.
package sdk

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"
)

// Driver beschreibt einen Treiber: einmal pro Gerätetyp registriert.
type Driver struct {
	Name         string          // = Paketname, nur [a-z0-9]
	ConfigSchema json.RawMessage // JSON-Schema für device_source.config; die UI baut daraus das Formular
	Actions      []ActionDef     // Katalog: wie jede angebotene Aktion funktioniert
	Facts        []FactDef       // Katalog der rohen Fakten (<Name>.<feld>)
	New          func(cfg SourceConfig) (Source, error)
}

// SourceConfig ist alles, was eine Source vom Core bekommt.
type SourceConfig struct {
	Address   string          // Management-Adresse; gehört dem User
	Config    json.RawMessage // device_source.config; gehört dem User, der Treiber ändert sie nie
	Secret    json.RawMessage // entschlüsselter Klartext des Secrets, leer ohne Secret; nie loggen
	Logger    *slog.Logger    // mit driver, source_id, device_id; der einzige Weg zu loggen
	Scheduler Scheduler       // für abfragende Treiber
}

// Scheduler verteilt periodische Arbeit abfragender Treiber über die Zeit und begrenzt die
// Zahl gleichzeitiger Aufrufe über alle Treiber.
type Scheduler interface {
	// Every ruft fn im Abstand interval auf, mit festem Versatz aus der ID der Quelle, bis ctx
	// endet; es kehrt sofort zurück. Läuft der vorige Aufruf noch, entfällt der Takt. Jeder
	// Aufruf bekommt einen ctx mit Timeout (Standard: interval). Fehler aus fn werden geloggt.
	Every(ctx context.Context, interval time.Duration, fn func(ctx context.Context) error)
}

// Source ist eine laufende Verbindung zu einem Gerät, eine pro Quelle.
type Source interface {
	// Heartbeat ist der erwartete Abstand der Lebenszeichen dieser Quelle; für die Lebensdauer
	// der Instanz fest. 0 = keine Überwachung (Sende-Ziel ohne Health-Check).
	Heartbeat() time.Duration
	// Run verbindet, meldet über r und verbindet bei Abbruch selbst neu, bis ctx endet. Danach
	// kehrt es zügig zurück und schließt alle Verbindungen. Ein Fehler führt zum Neustart.
	Run(ctx context.Context, r Reporter) error
	// Execute führt einen Befehl aus; parallel zu Run und für verschiedene Elemente gleichzeitig.
	// Ein error heißt: nicht erreicht (Lauf failed).
	Execute(ctx context.Context, c Command) (Result, error)
}

// Reporter nimmt die Meldungen einer Source entgegen. Aufrufe kommen nacheinander aus einer
// Goroutine; jeder zählt als Lebenszeichen. Der Reporter blockiert nicht lange.
type Reporter interface {
	Connected()
	Disconnected(err error)
	// Snapshot meldet den vollständigen Stand; nach jedem Connected zuerst, und bei jeder
	// Änderung der Struktur.
	Snapshot(s Snapshot)
	// Update meldet geänderte Fakten und Alarme.
	Update(u Update)
	// Heartbeat braucht nur, wer längere Zeit nichts zu melden hat.
	Heartbeat()
}
