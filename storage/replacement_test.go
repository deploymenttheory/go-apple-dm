package storage_test

import (
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/schema/checkin"
	"github.com/deploymenttheory/go-apple-dm/storage"
)

func TestReplacementAdmissionRejectsInvalidTransitions(t *testing.T) {
	now := time.Now()
	id := mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: "device"}
	for _, tc := range []struct {
		name   string
		change storage.ReplacementChange
		mutate func(*storage.Replacement, *storage.Enrollment)
	}{
		{"time required", storage.ReplacementChange{Op: "read"}, nil},
		{"unknown operation", storage.ReplacementChange{Op: "bogus"}, nil},
		{"attempt mismatch", storage.ReplacementChange{Op: "cancel", ID: "other"}, nil},
		{"disabled device", storage.ReplacementChange{Op: "cancel"}, func(_ *storage.Replacement, e *storage.Enrollment) { e.Enabled = false }},
		{"pin changed", storage.ReplacementChange{Op: "cancel"}, func(_ *storage.Replacement, e *storage.Enrollment) { e.CertHash = "different" }},
		{"terminal attempt", storage.ReplacementChange{Op: "cancel"}, func(r *storage.Replacement, _ *storage.Enrollment) { r.State = storage.ReplacementFailed }},
		{"wrong claim secret", storage.ReplacementChange{Op: "claim", SecretHash: "wrong", PublicKeyHash: "key"}, nil},
		{"different CSR replay", storage.ReplacementChange{Op: "claim", SecretHash: "secret", PublicKeyHash: "key", CSRHash: "csr"}, func(r *storage.Replacement, _ *storage.Enrollment) {
			r.PublicKeyHash = "key"
			r.CSRHash = "another CSR"
		}},
		{"wrong issuance method", storage.ReplacementChange{Op: "issue", Method: "acme", Hash: "new"}, nil},
		{"replayed certificate", storage.ReplacementChange{Op: "issue", Method: "scep", Hash: "new", PublicKeyHash: "key"}, func(r *storage.Replacement, _ *storage.Enrollment) {
			r.CandidateHash = "already-issued"
			r.PublicKeyHash = "key"
		}},
		{"unclaimed certificate", storage.ReplacementChange{Op: "issue", Method: "scep", Hash: "new", PublicKeyHash: "key"}, nil},
		{"wrong candidate", storage.ReplacementChange{Op: "authenticate", Hash: "wrong"}, nil},
		{"nil token", storage.ReplacementChange{Op: "token", Hash: "new"}, nil},
		{"invalid token", storage.ReplacementChange{Op: "token", Hash: "new", Token: &storage.ReplacementToken{ID: id, Message: &checkin.TokenUpdate{}}}, nil},
		{"foreign token", storage.ReplacementChange{Op: "token", Hash: "new", Token: &storage.ReplacementToken{ID: mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: "other"}, Message: &checkin.TokenUpdate{Topic: "t", Token: []byte{1}, PushMagic: "magic"}}}, nil},
		{"foreign command", storage.ReplacementChange{Op: "deliver", Hash: "unknown"}, nil},
		{"candidate download", storage.ReplacementChange{Op: "deliver", Hash: "new"}, nil},
		{"nil result", storage.ReplacementChange{Op: "result", Hash: "old"}, nil},
		{"unsent result", storage.ReplacementChange{Op: "result", Hash: "old", Response: &mdm.Response{CommandUUID: "attempt", Status: mdm.StatusAcknowledged}}, func(r *storage.Replacement, _ *storage.Enrollment) { r.Delivered = false }},
		{"unrelated result", storage.ReplacementChange{Op: "result", Hash: "new", Response: &mdm.Response{CommandUUID: "other", Status: mdm.StatusAcknowledged}}, nil},
		{"invalid result", storage.ReplacementChange{Op: "result", Hash: "old", Response: &mdm.Response{CommandUUID: "attempt", Status: mdm.StatusIdle}}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &storage.Replacement{
				ID:            "attempt",
				State:         storage.ReplacementPending,
				OldHash:       "old",
				Method:        "scep",
				SecretHash:    "secret",
				CandidateHash: "new",
				ExpiresAt:     now.Add(time.Minute),
				Authenticated: true,
				Delivered:     true,
				Command:       mdm.Command{UUID: "attempt"},
			}
			e := &storage.Enrollment{ID: id, Enabled: true, CertHash: "old"}
			if tc.mutate != nil {
				tc.mutate(r, e)
			}
			c := tc.change
			if tc.name != "time required" {
				c.At = now
			}
			if c.ID == "" {
				c.ID = "attempt"
			}
			if _, err := storage.AdvanceReplacement(&r, e, c); err == nil {
				t.Fatal("invalid transition accepted")
			}
		})
	}
	for _, status := range []mdm.Status{mdm.StatusNotNow, mdm.StatusCommandFormatError} {
		r := &storage.Replacement{
			ID:        "attempt",
			State:     storage.ReplacementPending,
			OldHash:   "old",
			ExpiresAt: now.Add(time.Minute),
			Delivered: true,
			Command:   mdm.Command{UUID: "attempt"},
		}
		if _, err := storage.AdvanceReplacement(
			&r,
			&storage.Enrollment{ID: id, Enabled: true, CertHash: "old"},
			storage.ReplacementChange{
				Op:       "result",
				ID:       "attempt",
				Hash:     "old",
				At:       now,
				Response: &mdm.Response{CommandUUID: "attempt", Status: status},
			},
		); err != nil {
			t.Fatal(err)
		}
	}
	var none *storage.Replacement
	if _, err := storage.AdvanceReplacement(
		&none,
		&storage.Enrollment{},
		storage.ReplacementChange{Op: "cancel", At: now},
	); err == nil {
		t.Fatal("absent attempt accepted")
	}
	if _, err := storage.AdvanceReplacement(
		&none,
		&storage.Enrollment{},
		storage.ReplacementChange{Op: "begin", At: now},
	); err == nil {
		t.Fatal("nil begin accepted")
	}
}
