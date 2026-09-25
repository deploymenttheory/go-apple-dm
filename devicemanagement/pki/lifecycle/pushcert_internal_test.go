package lifecycle

import (
	"errors"
	"fmt"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/fault"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/pki/pushcert"
)

// TestClassifyPushCert checks that each pushcert failure is pointed at its catalogued
// condition while the original sentinel stays matchable.
func TestClassifyPushCert(t *testing.T) {
	for name, tc := range map[string]struct {
		cause error
		want  *fault.Entry
	}{
		"NoTopic":     {fmt.Errorf("x: %w", pushcert.ErrNoTopic), fault.PushCertTopicMissing},
		"KeyMismatch": {pushcert.ErrKeyMismatch, fault.PushCertKeyMismatch},
		"Invalid":     {pushcert.ErrInvalid, fault.PushCertInvalid},
		"Other":       {errors.New("asn1: syntax error"), fault.PushCertInvalid},
	} {
		t.Run(name, func(t *testing.T) {
			err := classifyPushCert(tc.cause)
			if !errors.Is(err, tc.want) || !errors.Is(err, tc.cause) || fault.AudienceOf(err) != fault.Client {
				t.Fatalf("classified %v as %v", tc.cause, err)
			}
		})
	}
}
