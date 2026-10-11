package vocab

import "slices"

// Kern-Aktionen und bekannte Aktionen; rohe Aktionen tragen den Treibernamen als Präfix.
const (
	ActionStart         = "start"
	ActionStop          = "stop"
	ActionReboot        = "reboot"
	ActionSet           = "set"
	ActionTrigger       = "trigger"
	ActionResetCounters = "reset_counters"
)

var actions = []string{ActionStart, ActionStop, ActionReboot, ActionSet, ActionTrigger, ActionResetCounters}

// Action meldet, ob name eine Kern- oder bekannte Aktion ist.
func Action(name string) bool { return slices.Contains(actions, name) }

// namespaces ordnet jedem Namensraum von global_ref den Treiber zu, dem er gehört.
var namespaces = map[string]string{
	"xlink": "videoxlink",
	"nmos":  "nmos",
}

// NamespaceOwner liefert den Treiber, dem ein Namensraum von global_ref gehört.
func NamespaceOwner(namespace string) (driver string, ok bool) {
	driver, ok = namespaces[namespace]
	return driver, ok
}
