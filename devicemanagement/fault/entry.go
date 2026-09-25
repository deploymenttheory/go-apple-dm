package fault

import (
	"fmt"
	"log/slog"
	"regexp"
	"slices"
	"strings"
	"sync"
)

// Code names a specific failure condition. It is part of the published interface:
// clients branch on it, so a code is never reused for a different condition and never
// renamed.
//
// A client condition's code has the form DM-<DOMAIN>-<CONDITION>, upper case and hyphen
// separated. A device condition's code is the identifier Apple defines for the error
// document the device receives, such as com.apple.unrecognized.device, and is never
// reworded.
type Code string

// Audience is who has to act on a failure. It decides how the condition may be
// reported, so it is part of the declaration rather than a judgement made later.
type Audience int

const (
	// Operator failures mean the deployment is wrong: configuration, credentials,
	// storage, an Apple service that refuses this server's tokens. The person running
	// the server acts on them. They are reported in the terminal and the log, where
	// prose can name the setting and the remedy, and they are never published to a
	// client or a device, because neither can act on them and neither must be invited
	// to depend on a deployment's internal state.
	Operator Audience = iota + 1
	// Client failures mean the request is wrong. The caller of an API acts on them,
	// whether that caller is a person's tool or another service, so they carry a stable
	// code and a terse message that is safe to return.
	Client
	// Device failures are met by an Apple device following a protocol. The device acts
	// on the status and, where Apple defines one, on an error document with its own
	// code; it never receives this server's prose. A device condition therefore carries
	// Apple's code or none.
	Device
)

// String returns the audience's name, for logs and metrics.
func (a Audience) String() string {
	switch a {
	case Operator:
		return "operator"
	case Client:
		return "client"
	case Device:
		return "device"
	}
	return "unknown"
}

// Entry is a catalogued condition: a message for a person, a kind for any caller, an
// audience deciding how it may be reported, and — for client and device failures — a
// stable code. Values are declared once and used as sentinels, so they are compared by
// identity and never constructed at a call site.
type Entry struct {
	code     Code
	kind     *Kind
	audience Audience
	message  string
}

// NewOperator declares a condition the deployment's operator must act on. It takes no
// code: an operator failure is never published, so there is nothing for a client to
// branch on, and the absence is enforced here rather than checked later.
//
// Operator conditions are declared by the package that raises them. They carry no code,
// so there is no registry to keep and no reason to move them away from the code whose
// contract they belong to.
func NewOperator(kind *Kind, message string) *Entry {
	mustKind(kind)
	return &Entry{kind: kind, audience: Operator, message: message}
}

// NewClient declares a condition an API caller must act on. The code is required
// because it is the part of the response a client may rely on, and it must have the
// form DM-<DOMAIN>-<CONDITION>.
//
// Client conditions are declared in a catalogue file of the module that publishes
// them, one file per domain, because their codes are published: they must be unique,
// enumerable and reviewable as a set before they are promised to anyone. Declaring a
// code twice panics at initialisation, so a collision cannot reach a release.
func NewClient(code Code, kind *Kind, message string) *Entry {
	mustKind(kind)
	if !clientCode.MatchString(string(code)) {
		panic(fmt.Sprintf("fault: client code %q is not of the form DM-<DOMAIN>-<CONDITION>", code))
	}
	return register(&Entry{code: code, kind: kind, audience: Client, message: message})
}

// NewDevice declares a condition a device meets on a protocol route. The code is the
// identifier Apple assigns to the error document the device receives, or empty when
// Apple defines no document for the condition and the device acts on the status alone.
//
// Device conditions are catalogued alongside client conditions so the set a device can
// meet is enumerable, but their codes belong to Apple: this package never invents one.
func NewDevice(code Code, kind *Kind, message string) *Entry {
	mustKind(kind)
	e := &Entry{code: code, kind: kind, audience: Device, message: message}
	if code == "" {
		return e
	}
	if strings.HasPrefix(string(code), "DM-") {
		panic(fmt.Sprintf("fault: device code %q must be Apple's identifier, not a DM- code", code))
	}
	return register(e)
}

// Error returns the message. The code is not included: it is a field on the wire and a
// lookup for a client, not something a person reading a line needs to see.
func (e *Entry) Error() string { return e.message }

// Is reports the entry's kind, so errors.Is matches the general classification as well
// as the specific condition, which errors.Is checks by identity before calling Is.
func (e *Entry) Is(target error) bool { return target == e.kind }

// Code returns the catalogued code, which is empty for an operator condition and for a
// device condition without an Apple error document.
func (e *Entry) Code() Code { return e.code }

// Kind returns the catalogued classification.
func (e *Entry) Kind() *Kind { return e.kind }

// Audience returns who must act on the failure.
func (e *Entry) Audience() Audience { return e.audience }

// LogValue renders the condition as a group, so slog.Any("error", entry) yields fields a
// log query can match rather than a sentence.
func (e *Entry) LogValue() slog.Value { return valueOf(e) }

var clientCode = regexp.MustCompile(`^DM-[A-Z0-9]+(-[A-Z0-9]+)+$`)

// mustKind refuses a condition without a classification at declaration time.
func mustKind(kind *Kind) {
	if kind == nil {
		panic("fault: a condition needs a kind")
	}
}

var catalogue = struct {
	sync.Mutex
	byCode map[Code]*Entry
}{byCode: map[Code]*Entry{}}

// register records a coded entry. Declaring a code twice panics whatever the second
// declaration says, because two values for one code would let errors.Is against one
// miss a document parsed back into the other.
func register(e *Entry) *Entry {
	catalogue.Lock()
	defer catalogue.Unlock()
	if _, ok := catalogue.byCode[e.code]; ok {
		panic(fmt.Sprintf("fault: code %q declared twice", e.code))
	}
	catalogue.byCode[e.code] = e
	return e
}

// Catalogue returns every condition that carries a code, sorted by code. Operator
// conditions are absent by construction: they have no code to list.
func Catalogue() []*Entry {
	catalogue.Lock()
	defer catalogue.Unlock()
	out := make([]*Entry, 0, len(catalogue.byCode))
	for _, e := range catalogue.byCode {
		out = append(out, e)
	}
	slices.SortFunc(out, func(a, b *Entry) int { return strings.Compare(string(a.code), string(b.code)) })
	return out
}

// Lookup returns the catalogued condition for a code, so a client that received a
// problem document can turn its type back into the condition.
func Lookup(code Code) (*Entry, bool) {
	catalogue.Lock()
	defer catalogue.Unlock()
	e, ok := catalogue.byCode[code]
	return e, ok
}
