// Package coretest baut Welten in realistischer Größe für Tests und Benchmarks der Dienste, die
// die View des Core lesen.
package coretest

import (
	"fmt"
	"log/slog"

	"github.com/google/uuid"

	"github.com/lukirs95/monika/internal/core"
	"github.com/lukirs95/monika/internal/core/evaluation"
	"github.com/lukirs95/monika/internal/core/inventory"
	"github.com/lukirs95/monika/sdk"
	"github.com/lukirs95/monika/sdk/vocab"
)

// Driver ist der Treiber aller Quellen der Welt.
const Driver = "videoxlink"

// Device ist ein Gerät der Welt: eine Quelle mit Zugang, 3 Modulen und 96 Ports.
type Device struct {
	ID, Source, Access uuid.UUID
	Ports              []uuid.UUID
}

// World ist eine Welt mit Geräten, einer Gruppe pro 10 Geräte und der Gruppe All über allen.
type World struct {
	View    *core.View
	Devices []Device
	Groups  []uuid.UUID
	All     uuid.UUID
}

// NewWorld baut eine Welt mit devices Geräten zu je 100 Elementen; alle Quellen sind Live, jeder
// Port hat 4 Fakten und die Aktionen start und stop, Ports und Geräte haben Health.
func NewWorld(devices int) *World {
	w := &World{All: uuid.Must(uuid.NewV7())}
	var snap inventory.Snapshot
	facts := map[uuid.UUID]sdk.Facts{}
	health := map[uuid.UUID]evaluation.ElementHealth{}
	sources := map[uuid.UUID]core.SourceStatus{}
	snap.Elements = append(snap.Elements, inventory.Element{ID: w.All, Kind: inventory.KindGroup, Label: "all", Present: true, Monitored: true})
	for d := range devices {
		dev := Device{ID: uuid.Must(uuid.NewV7()), Source: uuid.Must(uuid.NewV7()), Access: uuid.Must(uuid.NewV7())}
		if d%10 == 0 {
			g := uuid.Must(uuid.NewV7())
			w.Groups = append(w.Groups, g)
			snap.Elements = append(snap.Elements, inventory.Element{ID: g, Kind: inventory.KindGroup, Present: true, Monitored: true})
			snap.Links = append(snap.Links, inventory.Link{ID: uuid.Must(uuid.NewV7()), Type: inventory.LinkMember, FromID: w.All, ToID: g})
		}
		snap.Links = append(snap.Links, inventory.Link{ID: uuid.Must(uuid.NewV7()), Type: inventory.LinkMember, FromID: w.Groups[len(w.Groups)-1], ToID: dev.ID})
		snap.Sources = append(snap.Sources, inventory.Source{ID: dev.Source, DeviceID: dev.ID, Driver: Driver, Enabled: true, AccessElementID: dev.Access})
		sources[dev.Source] = core.SourceStatus{State: core.StateLive}
		el := func(id, parent uuid.UUID, kind inventory.Kind, ext string, actions ...string) inventory.Element {
			return inventory.Element{ID: id, Kind: kind, ParentID: parent, DeviceID: dev.ID, SourceID: dev.Source,
				ExternalID: ext, Name: ext, Transport: "sdi", Actions: actions, Present: true, Monitored: true}
		}
		snap.Elements = append(snap.Elements,
			inventory.Element{ID: dev.ID, Kind: inventory.KindDevice, DeviceID: dev.ID, Label: fmt.Sprintf("D%d", d), Present: true, Monitored: true},
			el(dev.Access, dev.ID, inventory.KindAccess, "", vocab.ActionReboot))
		for m := range 3 {
			mid := uuid.Must(uuid.NewV7())
			snap.Elements = append(snap.Elements, el(mid, dev.Access, vocab.ElementModule, fmt.Sprintf("m/%d", m)))
			for p := range 32 {
				pid := uuid.Must(uuid.NewV7())
				snap.Elements = append(snap.Elements, el(pid, mid, vocab.ElementPort, fmt.Sprintf("p/%d/%d", m, p), vocab.ActionStart, vocab.ActionStop))
				dev.Ports = append(dev.Ports, pid)
				facts[pid] = sdk.Facts{
					vocab.StateRunning: true, vocab.StateSignalPresent: true,
					"sdi.crc_errors": int64(p), "video.frame_rate": 50.0,
				}
				health[pid] = evaluation.ElementHealth{ElementID: pid, Health: evaluation.HealthOK}
			}
		}
		health[dev.ID] = evaluation.ElementHealth{ElementID: dev.ID, Health: evaluation.HealthOK, Rollup: &evaluation.Rollup{OK: 96, Unchecked: 4}}
		w.Devices = append(w.Devices, dev)
	}
	inv := inventory.New(nil, nil, slog.New(slog.DiscardHandler))
	inv.Load(snap)
	w.View = &core.View{Graph: inv.Graph(), Facts: facts, Health: health, Sources: sources, DB: core.DBStatus{OK: true}}
	return w
}

// Sizes sind die Stufen der Benchmarks (Geräte).
var Sizes = []int{10, 50, 100, 200, 400, 1000}
