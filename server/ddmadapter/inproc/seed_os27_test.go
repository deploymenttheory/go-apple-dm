//go:build schema_seed_os_27

package inproc_test

import (
	"fmt"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/ddm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/paging"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/status"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/storage/ddm/inmem"
	"github.com/deploymenttheory/go-apple-dm/server/ddmadapter/inproc"
)

func TestSeedOS27EnhancedLoggingStatus(t *testing.T) {
	t.Parallel()
	engine, err := ddm.New(ddm.Config{Store: inmem.New()})
	if err != nil {
		t.Fatal(err)
	}
	handler := inproc.Handler(engine)
	id := mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: "D1"}
	for _, state := range []string{"none", "waiting-for-consent", "collecting", "follow-up-question", "upload-consent", "uploading", "finished", "failed", "cancelled", "declined"} {
		body := []byte(fmt.Sprintf(`{"StatusItems":{"enhanced-logging":{"status":%q,"applecare-token":"test-token-normal","timestamp":"2026-09-12T12:00:00Z"}}}`, state))
		ck, m := dmCheckin(t, id.ID, "status", body)
		if res, err := handler(t.Context(), &mdm.Request{}, ck, m); err != nil || res.Status != 200 {
			t.Fatal(res, err)
		}
		rows, err := engine.StatusValues(t.Context(), id, ddm.StatusValueQuery{PathPrefix: "enhanced-logging."}, paging.Page{})
		if err != nil || len(rows.Items) != 3 {
			t.Fatal(rows, err)
		}
		want := map[string]string{status.StatusItemTypeEnhancedLoggingStatus: fmt.Sprintf("%q", state), status.StatusItemTypeEnhancedLoggingAppleCareToken: `"test-token-normal"`, status.StatusItemTypeEnhancedLoggingTimestamp: `"2026-09-12T12:00:00Z"`}
		for _, row := range rows.Items {
			if string(row.Value) != want[row.Path] {
				t.Fatalf("state %s: %+v", state, row)
			}
		}
	}
	// A full report removes omitted values; a partial report preserves them.
	for _, full := range []bool{false, true} {
		ck, m := dmCheckin(t, id.ID, "status", []byte(fmt.Sprintf(`{"FullReport":%t,"StatusItems":{}}`, full)))
		if _, err := handler(t.Context(), &mdm.Request{}, ck, m); err != nil {
			t.Fatal(err)
		}
		rows, err := engine.StatusValues(t.Context(), id, ddm.StatusValueQuery{}, paging.Page{})
		if err != nil {
			t.Fatal(err)
		}
		want := 3
		if full {
			want = 0
		}
		if len(rows.Items) != want {
			t.Fatal(rows)
		}
	}
}
