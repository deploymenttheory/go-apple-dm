package lab

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/commands"
)

// TestCommandWaitsForAcceptedWakeAfterCoalescing requires a real accepted wake after a skipped push.
func TestCommandWaitsForAcceptedWakeAfterCoalescing(t *testing.T) {
	cmd, err := mdm.NewCommand(&commands.ProfileList{})
	if err != nil {
		t.Fatal(err)
	}
	pushes := 0
	e := &Environment{Instance: Instance{URL: "https://lab.invalid"}, Client: &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		body := `{"Queued":1}`
		switch {
		case strings.HasSuffix(r.URL.Path, "/push"):
			pushes++
			body = `{"Sent":false,"Outcome":"skipped"}`
			if pushes == 2 {
				body = `{"Sent":true,"Outcome":"sent","Status":200}`
			}
		case strings.HasSuffix(r.URL.Path, "/result"):
			if pushes != 2 {
				t.Fatal("polled command before accepted wake")
			}
			b, _ := json.Marshal(map[string]any{"Status": "Acknowledged", "Response": []byte("reply")})
			body = string(b)
		}
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}, nil
	})}}
	dir := t.TempDir()
	if _, err = Command(t.Context(), e, "/enrollments/device/mac", cmd, dir); err != nil {
		t.Fatal(err)
	}
	// #nosec G304 -- Temp directory and locally generated command UUID.
	b, err := os.ReadFile(filepath.Join(dir, cmd.UUID+"-push-attempts.json"))
	if err != nil {
		t.Fatal(err)
	}
	var attempts []struct {
		Sent    bool   `json:"Sent"`
		Outcome string `json:"Outcome"`
		Status  int    `json:"Status"`
	}
	if err = json.Unmarshal(b, &attempts); err != nil {
		t.Fatal(err)
	}
	if len(attempts) != 2 || attempts[0].Sent || attempts[0].Outcome != "skipped" || !attempts[1].Sent || attempts[1].Status != 200 {
		t.Fatalf("lost push evidence: %s", b)
	}
}

// TestCommandSkippedWakeCannotPass rejects coalesced pushes without polling for a command result.
func TestCommandSkippedWakeCannotPass(t *testing.T) {
	cmd, err := mdm.NewCommand(&commands.ProfileList{})
	if err != nil {
		t.Fatal(err)
	}
	e := &Environment{Instance: Instance{URL: "https://lab.invalid"}, Client: &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		body := `{"Queued":1}`
		if strings.HasSuffix(r.URL.Path, "/push") {
			body = `{"Sent":false,"Outcome":"skipped"}`
		}
		if strings.HasSuffix(r.URL.Path, "/result") {
			t.Fatal("unaccepted wake reached acknowledgment polling")
		}
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}, nil
	})}}
	ctx, cancel := context.WithTimeout(t.Context(), 25*time.Millisecond)
	defer cancel()
	if _, err = Command(ctx, e, "/enrollments/device/mac", cmd, t.TempDir()); err == nil {
		t.Fatal("skipped wake passed")
	}
}
