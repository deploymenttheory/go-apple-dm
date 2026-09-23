package target

import (
	"context"
	"errors"
	"slices"
)

// Target kinds. Simulator targets are driven by the server-side protocol simulator;
// device targets are real Apple devices, virtual or physical.
const (
	KindSimulator = "simulator"
	KindDevice    = "device"
)

// Capability names reported by Target.Capabilities and required by lab modules.
const (
	CapExec       = "exec"        // run commands on the device
	CapUserScript = "user-script" // run osascript in the logged-in user's session
	CapOpenURL    = "open-url"    // open a URL in the device's default browser
	CapScreenshot = "screenshot"  // capture the device display
	CapCheckpoint = "checkpoint"  // save and restore complete device state
)

// ErrUnsupported reports an operation the target's driver cannot perform.
var ErrUnsupported = errors.New("target: operation unsupported")

// Info describes a target for applicability decisions and result evidence.
//
// Evidence JSON uses the administration PascalCase convention.
type Info struct {
	ID           string `json:"ID"`
	Driver       string `json:"Driver"`
	Kind         string `json:"Kind"`
	Platform     string `json:"Platform,omitempty"`
	OSVersion    string `json:"OSVersion,omitempty"`
	BuildVersion string `json:"BuildVersion,omitempty"`
	Model        string `json:"Model,omitempty"`
	UDID         string `json:"UDID,omitempty"`
	Serial       string `json:"Serial,omitempty"`
	UserID       string `json:"UserID,omitempty"`
	Virtual      bool   `json:"Virtual,omitempty"`
}

// Command is a device command. User selects the logged-in console user instead of root.
type Command struct {
	Args  []string
	User  bool
	Stdin []byte
}

// Output is a completed device command.
type Output struct {
	Stdout, Stderr []byte
	ExitCode       int
}

// Target is the device-side contract used by lab modules.
type Target interface {
	// Describe returns the current device description.
	Describe(ctx context.Context) (Info, error)
	// Exec runs a command and returns its output; a nonzero exit is not an error.
	Exec(ctx context.Context, c Command) (Output, error)
	// UserScript runs AppleScript in the logged-in user's graphical session.
	UserScript(ctx context.Context, osascript string) (Output, error)
	// OpenURL opens the URL in the device's default browser.
	OpenURL(ctx context.Context, url string) error
	// Screenshot writes a PNG of the device display to dst.
	Screenshot(ctx context.Context, dst string) error
	// Checkpoint saves complete device state under name.
	Checkpoint(ctx context.Context, name string) error
	// Restore returns the device to a saved checkpoint.
	Restore(ctx context.Context, name string) error
	// Capabilities lists the operations this target performs.
	Capabilities() []string
}

// Has reports whether t supports every named capability, returning the first missing one.
func Has(t Target, required ...string) (string, bool) {
	caps := t.Capabilities()
	for _, name := range required {
		if !slices.Contains(caps, name) {
			return name, false
		}
	}
	return "", true
}

// Unsupported implements every device operation as ErrUnsupported. Drivers embed it and
// override the operations they perform.
type Unsupported struct{}

// Exec returns ErrUnsupported.
func (Unsupported) Exec(context.Context, Command) (Output, error) { return Output{}, ErrUnsupported }

// UserScript returns ErrUnsupported.
func (Unsupported) UserScript(context.Context, string) (Output, error) {
	return Output{}, ErrUnsupported
}

// OpenURL returns ErrUnsupported.
func (Unsupported) OpenURL(context.Context, string) error { return ErrUnsupported }

// Screenshot returns ErrUnsupported.
func (Unsupported) Screenshot(context.Context, string) error { return ErrUnsupported }

// Checkpoint returns ErrUnsupported.
func (Unsupported) Checkpoint(context.Context, string) error { return ErrUnsupported }

// Restore returns ErrUnsupported.
func (Unsupported) Restore(context.Context, string) error { return ErrUnsupported }

// Capabilities returns no capabilities.
func (Unsupported) Capabilities() []string { return nil }

// Simulator is the target for simulated workspaces. Modules create protocol simulator
// devices through the lab environment, so it performs no device operations itself.
type Simulator struct{ Unsupported }

// Describe identifies the simulator target.
func (Simulator) Describe(context.Context) (Info, error) {
	return Info{ID: KindSimulator, Driver: KindSimulator, Kind: KindSimulator}, nil
}

// Attached is a device the operator enrolled by hand and identifies by its enrollment
// identifiers. The lab observes it only through the server.
type Attached struct {
	Unsupported
	// UDID is the device enrollment identifier; UserID is the installing user's
	// GeneratedUID for user-channel modules.
	UDID, UserID string
}

// Describe identifies the attached device; the platform is learned through the server.
func (a Attached) Describe(context.Context) (Info, error) {
	return Info{ID: "attached", Driver: "attached", Kind: KindDevice, UDID: a.UDID, UserID: a.UserID}, nil
}
