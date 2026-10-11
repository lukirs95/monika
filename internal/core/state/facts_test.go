package state

import (
	"reflect"
	"testing"

	"github.com/google/uuid"

	"github.com/lukirs95/monika/sdk"
)

func TestFacts(t *testing.T) {
	src, other := uuid.New(), uuid.New()
	port, sender, fan := uuid.New(), uuid.New(), uuid.New()
	f := NewFacts()

	got := f.Replace(src, map[uuid.UUID]sdk.Facts{
		port:   {"state.signal_present": true, "sdi.level": "3G-A"},
		sender: {"state.running": true},
	})
	want := Changed{port: {"sdi.level", "state.signal_present"}, sender: {"state.running"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Replace = %v, want %v", got, want)
	}
	f.Replace(other, map[uuid.UUID]sdk.Facts{fan: {"system.fan_speed_rpm": int64(3000)}})

	t.Run("merge reports only real changes", func(t *testing.T) {
		got := f.Merge(src, map[uuid.UUID]sdk.Facts{
			port:   {"state.signal_present": false, "sdi.level": "3G-A"},
			sender: {"state.running": true},
		})
		if want := (Changed{port: {"state.signal_present"}}); !reflect.DeepEqual(got, want) {
			t.Fatalf("Merge = %v, want %v", got, want)
		}
		if v, _ := f.Get(port, "sdi.level"); v != "3G-A" {
			t.Fatalf("merge dropped an untouched key: %v", f.Element(port))
		}
	})

	t.Run("facts are copy-on-write", func(t *testing.T) {
		before := f.Element(port)
		snap := f.Snapshot()
		f.Merge(src, map[uuid.UUID]sdk.Facts{port: {"sdi.level": "HD"}})
		if before["sdi.level"] != "3G-A" || snap[port]["sdi.level"] != "3G-A" {
			t.Fatal("an earlier view changed after Merge")
		}
	})

	t.Run("replace removes what the snapshot lacks", func(t *testing.T) {
		got := f.Replace(src, map[uuid.UUID]sdk.Facts{port: {"state.signal_present": false, "sdi.level": "HD"}})
		if want := (Changed{sender: {"state.running"}}); !reflect.DeepEqual(got, want) {
			t.Fatalf("Replace = %v, want %v", got, want)
		}
		if _, ok := f.Get(sender, "state.running"); ok {
			t.Fatal("facts of an element missing from the snapshot are still there")
		}
		if _, ok := f.Get(fan, "system.fan_speed_rpm"); !ok {
			t.Fatal("Replace touched another source")
		}
	})

	t.Run("drop source", func(t *testing.T) {
		got := f.DropSource(src)
		if want := (Changed{port: {"sdi.level", "state.signal_present"}}); !reflect.DeepEqual(got, want) {
			t.Fatalf("DropSource = %v, want %v", got, want)
		}
		if len(f.Snapshot()) != 1 {
			t.Fatalf("Snapshot = %v, want only the fan", f.Snapshot())
		}
	})
}
