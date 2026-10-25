package storagetest

import (
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/appleplatformservices/appsbooks"
)

// runLicensingValidation checks invalid intents, stale revisions and unrelated authenticated callbacks.
func runLicensingValidation(t *testing.T, factory AppsBooksFactory) {
	t.Helper()
	a, _ := factory(t)
	now := &atomic.Int64{}
	now.Store(time.Now().UnixNano())
	f, cfg, token := licensingFixture(t)
	w, err := appsbooks.NewWorkflow(&jobTimeStore{Store: a, now: now}, cfg)
	failAppsBooksIf(t, err != nil, err)
	l, err := w.Register(t.Context(), "location", 0, token)
	failAppsBooksIf(t, err != nil, err)
	invalid := []appsbooks.JobRequest{{}, {Operation: "unknown", MaxPages: 1}, {Operation: appsbooks.JobAssociate, MaxPages: 1}, {Operation: appsbooks.JobAssociate, MaxPages: 1, Assets: &appsbooks.ManageAssetsRequest{}}, {Operation: appsbooks.JobRevoke, MaxPages: 1}, {Operation: appsbooks.JobCreateUsers, MaxPages: 1}, {Operation: appsbooks.JobCreateUsers, MaxPages: 1, Users: &appsbooks.ManageUsersRequest{Users: []appsbooks.RequestUser{{ClientUserID: ""}}}}}
	bad := jobRequest()
	bad.Assets.Assets[0].AdamID = ""
	invalid = append(invalid, bad)
	for _, request := range invalid {
		_, err := w.CreateJob(t.Context(), l.ID, l.Revision, "bad", request)
		failAppsBooksIf(t, !errors.Is(err, appsbooks.ErrInput), request, err)
	}
	_, err = w.CreateJob(t.Context(), l.ID, l.Revision, "", jobRequest())
	failAppsBooksIf(t, !errors.Is(err, appsbooks.ErrInput), err)
	_, err = w.CreateJob(t.Context(), l.ID, l.Revision+1, "stale", jobRequest())
	failAppsBooksIf(t, !errors.Is(err, appsbooks.ErrRevision), err)
	j, err := w.CreateJob(t.Context(), l.ID, l.Revision, "job", jobRequest())
	failAppsBooksIf(t, err != nil, err)
	_, err = w.SubmitJob(t.Context(), l.ID, j.ID, "", time.Minute)
	failAppsBooksIf(t, !errors.Is(err, appsbooks.ErrInput), err)
	_, err = w.PollJob(t.Context(), l.ID, j.ID, "poll", time.Minute)
	failAppsBooksIf(t, !errors.Is(err, appsbooks.ErrReconciliation), err)
	_, err = w.RecoverLicensingToken(t.Context(), l.ID, 0, j.ID, token)
	failAppsBooksIf(t, !errors.Is(err, appsbooks.ErrInput), err)
	_, err = w.ReconcileJob(t.Context(), l.ID, j.ID, "", "", false)
	failAppsBooksIf(t, !errors.Is(err, appsbooks.ErrInput), err)
	_, err = w.ReconcileJob(t.Context(), l.ID, j.ID, "event", "operator", true)
	failAppsBooksIf(t, !errors.Is(err, appsbooks.ErrInput), err)
	for i, body := range []string{`{"eventId":1}`, `{}`, `{"eventId":"event","type":"ASSOCIATE","result":2}`} {
		for _, kind := range []string{"ASSET_MANAGEMENT", "USER_MANAGEMENT"} {
			text := fmt.Sprintf(`{"notificationId":%q,"notificationType":%q,"uId":"UID","notification":%s}`, fmt.Sprint(i), kind, body)
			_, err := deliverJobCallback(t, w, l.ID, text)
			failAppsBooksIf(t, !errors.Is(err, appsbooks.ErrProtocol), text, err)
		}
	}
	j, err = w.SubmitJob(t.Context(), l.ID, j.ID, "worker", time.Minute)
	failAppsBooksIf(t, err != nil, err)
	for _, body := range []string{
		jobCallback("wrongasset", "event", "SUCCESS", "s1"),
		strings.Replace(jobCallback("both", "event", "SUCCESS", "s1"), `"serialNumber":"s1"`, `"serialNumber":"s1","clientUserId":"u"`, 1),
		`{"notificationId":"wrongkind","notificationType":"USER_MANAGEMENT","uId":"UID","notification":{"eventId":"event","result":"SUCCESS","type":"ASSOCIATE","users":[]}}`,
	} {
		if strings.Contains(body, "wrongasset") {
			body = strings.Replace(body, `"adamId":"1"`, `"adamId":"2"`, 1)
		}
		_, err := deliverJobCallback(t, w, l.ID, body)
		failAppsBooksIf(t, !errors.Is(err, appsbooks.ErrProtocol), body, err)
	}
	f.mu.Lock()
	f.status = "FAILED"
	f.failure = true
	f.mu.Unlock()
	now.Add(int64(5 * time.Minute))
	j, err = w.PollJob(t.Context(), l.ID, j.ID, "poll", time.Minute)
	failAppsBooksIf(t, err != nil || j.State != appsbooks.JobFailed, j, err)
	_, err = w.ReconcileJob(t.Context(), l.ID, j.ID, "", "terminal", false)
	failAppsBooksIf(t, !errors.Is(err, appsbooks.ErrInput), err)
	// A stale queued intent cannot submit after any successful location revision.
	old, err := w.CreateJob(t.Context(), l.ID, l.Revision, "old", jobRequest())
	failAppsBooksIf(t, err != nil, err)
	l, err = w.Probe(t.Context(), l.ID, l.Revision)
	failAppsBooksIf(t, err != nil, err)
	_, err = w.SubmitJob(t.Context(), l.ID, old.ID, "worker", time.Minute)
	failAppsBooksIf(t, !errors.Is(err, appsbooks.ErrRevision), err)
}
