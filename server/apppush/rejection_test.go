package apppush_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/state"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/testpki"
	"github.com/deploymenttheory/go-apple-dm/server/apppush"
)

func TestCredentialRejectsUnstorableTopic(t *testing.T) {
	t.Parallel()
	ca, err := testpki.NewCA("issuer")
	if err != nil {
		t.Fatal(err)
	}
	topic := "com.example." + strings.Repeat("a", 1024)
	id, err := ca.IssueApp(topic, time.Now().Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	cert, key, err := id.PEM()
	if err != nil {
		t.Fatal(err)
	}
	st := state.NewMemory()
	s := apppush.Store{State: st}
	if _, err := s.Put(t.Context(), topic, cert, key); !errors.Is(err, state.ErrInvalid) {
		t.Fatalf("unstorable topic accepted: %v", err)
	}
	rows, err := st.List(t.Context(), "apppush/", "", 100)
	if err != nil || len(rows) != 0 {
		t.Fatalf("rejected import mutated store: %v %v", rows, err)
	}
}
