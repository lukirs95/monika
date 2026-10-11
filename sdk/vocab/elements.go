package vocab

import "slices"

// Arten von Elementen (kind), die Treiber melden; dazu die reservierten in vocab.go.
const (
	ElementModule           = "module"
	ElementPort             = "port"
	ElementNetworkInterface = "network_interface"
	ElementClock            = "clock"
	ElementSender           = "sender"
	ElementReceiver         = "receiver"
	ElementPeer             = "peer"
	ElementPath             = "path"
	ElementTunnel           = "tunnel"
	ElementProcessing       = "processing"
	ElementSensor           = "sensor"
	ElementTrigger          = "trigger"
)

var elementKinds = []string{
	ElementDevice, ElementGroup, ElementAccess,
	ElementModule, ElementPort, ElementNetworkInterface, ElementClock, ElementSender, ElementReceiver,
	ElementPeer, ElementPath, ElementTunnel, ElementProcessing, ElementSensor, ElementTrigger,
}

// ElementKind meldet, ob kind eine bekannte Art ist (auch die reservierten).
func ElementKind(kind string) bool { return slices.Contains(elementKinds, kind) }

// Richtungen eines Anschlusses: die Fähigkeit, nicht die aktuelle Richtung.
const (
	DirectionIn    = "in"
	DirectionOut   = "out"
	DirectionBidir = "bidir"
)

// Direction meldet, ob d eine gültige Richtung ist; "" heißt „keine Angabe“.
func Direction(d string) bool {
	return d == "" || d == DirectionIn || d == DirectionOut || d == DirectionBidir
}

// transports ist die Liste der Transporte aus docs/contracts/vocabulary.md.
var transports = []string{
	"sdi", "hdmi", "dvi", "displayport", "hdbaset", "composite", "svideo", "component",
	"analog", "aes3", "aes3id", "spdif", "aes42", "madi", "adat", "aes50", "tdif",
	"ultranet", "optocore", "rocknet", "ethersound", "cobranet", "anet",
	"aes67", "dante", "ravenna", "milan", "livewire",
	"st2022-6", "ndi",
	"st2110-20", "st2110-22", "st2110-30", "st2110-31", "st2110-40", "st2110-41",
	"asi", "st2022-2",
	"xlink", "srt", "rist", "rtmp", "zixi", "whip",
	"ethernet",
	"ptp", "blackburst", "trilevel", "wordclock", "dars",
	"ltc",
	"4wire", "2wire",
	"rs422", "rs232", "gpio",
}

// Transport meldet, ob t ein bekannter Transport ist; "" heißt „keine Angabe“.
func Transport(t string) bool { return t == "" || slices.Contains(transports, t) }

// Transports liefert alle bekannten Transporte.
func Transports() []string { return append([]string(nil), transports...) }
