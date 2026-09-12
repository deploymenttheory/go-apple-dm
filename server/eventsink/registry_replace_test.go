package eventsink_test

import (
	"sync"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/mdmprotocol/event"
	"github.com/deploymenttheory/go-apple-dm/server/eventsink"
)

func TestReplaceProjectionWithMetadataOnly(t *testing.T) {
	r := eventsink.NewRegistry()
	e := event.Event{Type: event.Enrolled, Data: "fixture"}
	project := func(v any) map[string]any { return map[string]any{"allowed": v} }
	r.Register(e.Type, project)
	if len(r.Project(e).Fields) != 1 {
		t.Fatal("missing projection")
	}
	r.Register(e.Type, nil)
	if !r.Known(e.Type) || len(r.Types()) != 1 || r.Project(e).Fields != nil {
		t.Fatal("nil registration retained payload or forgot type")
	}
	r.Register(e.Type, project)
	if len(r.Project(e).Fields) != 1 {
		t.Fatal("replacement not restored")
	}
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			for range 100 {
				_ = r.Project(e)
				_ = r.Known(e.Type)
				_ = r.Types()
			}
		})
	}
	for range 100 {
		r.Register(e.Type, nil)
		r.Register(e.Type, project)
	}
	wg.Wait()
	r.Register(e.Type, nil)
	if r.Project(e).Fields != nil {
		t.Fatal("final projection retained payload")
	}
}
