package apns_test

import (
	"sort"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/v3/appleplatformservices/push/apns"
)

// The constants and the table are one set. A constant missing from the table
// would be a reason a caller could name but not bound, and a table entry with
// no constant would be a reason nothing in the tree can refer to.
func TestReasonVocabulary(t *testing.T) {
	t.Parallel()
	consts := []string{
		apns.ReasonBadCollapseID, apns.ReasonBadDeviceToken, apns.ReasonBadExpirationDate,
		apns.ReasonBadMessageID, apns.ReasonBadPriority, apns.ReasonBadTopic,
		apns.ReasonDeviceTokenNotForTopic, apns.ReasonDuplicateHeaders, apns.ReasonIdleTimeout,
		apns.ReasonInvalidPushType, apns.ReasonMissingDeviceToken, apns.ReasonMissingTopic,
		apns.ReasonPayloadEmpty, apns.ReasonTopicDisallowed, apns.ReasonBadCertificate,
		apns.ReasonBadCertificateEnvironment, apns.ReasonExpiredProviderToken, apns.ReasonForbidden,
		apns.ReasonInvalidProviderToken, apns.ReasonMissingProviderToken,
		apns.ReasonUnrelatedKeyIDInToken, apns.ReasonBadEnvironmentKeyIDInToken,
		apns.ReasonBadPath, apns.ReasonMethodNotAllowed, apns.ReasonExpiredToken,
		apns.ReasonUnregistered, apns.ReasonPayloadTooLarge,
		apns.ReasonTooManyProviderTokenUpdates, apns.ReasonTooManyRequests,
		apns.ReasonInternalServerError, apns.ReasonServiceUnavailable, apns.ReasonShutdown,
	}
	if len(apns.Reasons) != len(consts) {
		t.Fatalf("Reasons has %d entries, %d constants", len(apns.Reasons), len(consts))
	}
	for _, code := range consts {
		info, ok := apns.Reasons[code]
		if !ok {
			t.Errorf("constant %q is not in Reasons", code)
			continue
		}
		if info.Code != code || info.Status == 0 || info.Description == "" {
			t.Errorf("entry for %q is incomplete: %+v", code, info)
		}
	}
	got := apns.ReasonCodes()
	if !sort.StringsAreSorted(got) || len(got) != len(consts) {
		t.Fatalf("ReasonCodes() = %d entries, sorted=%v", len(got), sort.StringsAreSorted(got))
	}
	if _, ok := apns.Reasons["NoSuchReason"]; ok {
		t.Error("Reasons contains a reason APNs is not documented to send")
	}
}

// Why pushOne inspects the reason at all: an invalid token does not always
// arrive as 410. Apple documents BadDeviceToken and DeviceTokenNotForTopic
// with status 400, which is otherwise the shape of a retryable failure, so
// without the reason the client would retry a token that can never work.
func TestInvalidTokenReasonsArriveAsBadRequest(t *testing.T) {
	t.Parallel()
	for _, code := range []string{apns.ReasonBadDeviceToken, apns.ReasonDeviceTokenNotForTopic} {
		if got := apns.Reasons[code].Status; got != 400 {
			t.Errorf("%s: status %d, want 400", code, got)
		}
	}
	// The 410 reasons need no special case; the status alone is conclusive.
	for _, code := range []string{apns.ReasonExpiredToken, apns.ReasonUnregistered} {
		if got := apns.Reasons[code].Status; got != 410 {
			t.Errorf("%s: status %d, want 410", code, got)
		}
	}
}
