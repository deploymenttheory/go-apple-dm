package app

import (
	"context"
	"crypto/x509/pkix"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/clock"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/correlation"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/inventory"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/cms"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/plist"
	authority "github.com/deploymenttheory/go-apple-dm/devicemanagement/pki/ca"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/pki/revocation"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/commands"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/secrets"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/storage"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/testpki"
)

// TestInventoryReplacementWaitsForActivePin exercises the real App observers
// throughout the candidate Authenticate, TokenUpdate and command-ack handshake.
func TestInventoryReplacementWaitsForActivePin(t *testing.T) {
	for _, backend := range []string{"inmem", "sqlite"} {
		t.Run(backend, func(t *testing.T) {
			ctx := t.Context()
			at := time.Now().UTC()
			ca, err := testpki.NewCA("replacement")
			if err != nil {
				t.Fatal(err)
			}
			cfg := Config{Storage: backend, Clock: clock.NewFake(at), Enroll: EnrollConfig{
				PublicURL: "https://mdm.example", Topic: "com.apple.mgmt.test",
				Admission: func(context.Context, AdmissionRequest) (AdmissionGrant, error) {
					return AdmissionGrant{ExpiresAt: at.Add(time.Hour)}, nil
				},
			}}
			if backend == "sqlite" {
				cfg.DSN = filepath.Join(t.TempDir(), "replacement.db")
				cfg.StorageKeys = []string{"test"}
				cfg.Secrets = secrets.Static{"test": []byte("0123456789abcdef0123456789abcdef")}
			}
			appCert, appKey, err := authority.NewSelfSigned(authority.SelfSignedOptions{Subject: pkix.Name{CommonName: "app CA"}})
			if err != nil {
				t.Fatal(err)
			}
			certPEM, keyPEM, err := (&testpki.Identity{Cert: appCert, Key: appKey}).PEM()
			if err != nil {
				t.Fatal(err)
			}
			cfg.Enroll.CACertFile = filepath.Join(t.TempDir(), "ca.pem")
			cfg.Enroll.CAKeyFile = filepath.Join(t.TempDir(), "key.pem")
			if err := os.WriteFile(cfg.Enroll.CACertFile, certPEM, 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(cfg.Enroll.CAKeyFile, keyPEM, 0o600); err != nil {
				t.Fatal(err)
			}
			cfg.Enroll.SCEPHMACKey = []byte("0123456789abcdef0123456789abcdef")
			a, err := Build(ctx, cfg)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = a.Close() })
			id := mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: "replacement-device"}
			ca.Identity = testpki.Identity{Cert: appCert, Key: appKey}
			old, err := ca.Issue("old", at.Add(-time.Minute))
			if err != nil {
				t.Fatal(err)
			}
			candidate, err := ca.Issue("candidate", at.Add(-time.Minute))
			if err != nil {
				t.Fatal(err)
			}
			for _, identity := range []*testpki.Identity{old, candidate} {
				if err := a.revocations.Register(ctx, cms.Fingerprint(appCert), identity.Cert, revocation.Provenance{Source: IdentityACME, EnrollmentID: id.ID, UDID: id.ID, Serial: "SER"}); err != nil {
					t.Fatal(err)
				}
			}
			oldHash, newHash := cms.Fingerprint(old.Cert), cms.Fingerprint(candidate.Cert)
			if err := a.Store.Import(ctx, storage.EnrollmentExport{Enrollment: storage.Enrollment{ID: id, Enabled: true, CertHash: oldHash, EnrolledAt: at, Device: storage.DeviceInfo{SerialNumber: "SER"}}}); err != nil {
				t.Fatal(err)
			}
			setupJSON(t, a.protocol, "issued-identity:"+newHash, identityEvidence{EnrollmentMethod: correlation.AutomatedDeviceEnrollment, UDID: id.ID, Serial: "SER", NotAfter: candidate.Cert.NotAfter})
			command, err := mdm.NewCommand(&commands.InstallProfile{Payload: []byte("profile")}, mdm.WithUUID("replace"))
			if err != nil {
				t.Fatal(err)
			}
			transitions := a.replacementStore()
			if _, err := transitions.TransitionReplacement(ctx, id, storage.ReplacementChange{Op: "begin", At: at, Begin: &storage.Replacement{ID: "replace", Method: "acme", OldHash: oldHash, ExpiresAt: at.Add(10 * time.Minute), Command: *command}}); err != nil {
				t.Fatal(err)
			}
			if _, err := transitions.TransitionReplacement(ctx, id, storage.ReplacementChange{Op: "issue", ID: "replace", Method: "acme", Hash: newHash, At: at}); err != nil {
				t.Fatal(err)
			}
			connect := func(cert bool, status mdm.Status, uuid string) *mdm.Command {
				t.Helper()
				raw, err := plist.Marshal(map[string]any{"Status": string(status), "UDID": id.ID, "CommandUUID": uuid})
				if err != nil {
					t.Fatal(err)
				}
				response, err := mdm.DecodeResponse(raw, "")
				if err != nil {
					t.Fatal(err)
				}
				identity := old.Cert
				if cert {
					identity = candidate.Cert
				}
				result, err := a.Core.Connect(ctx, &mdm.Request{Certificate: identity}, response)
				if err != nil {
					t.Fatal(err)
				}
				return result
			}
			if got := connect(false, mdm.StatusIdle, ""); got == nil || got.UUID != command.UUID {
				t.Fatal("replacement command not delivered", got)
			}
			certificateRef := inventory.EnrollmentReference("mdm.certificate", id)
			certificateHistory, err := a.Inventory.History(ctx, certificateRef)
			if err != nil {
				t.Fatal(err)
			}
			checkin := func(body map[string]any) []byte {
				t.Helper()
				body["UDID"] = id.ID
				raw, err := plist.Marshal(body)
				if err != nil {
					t.Fatal(err)
				}
				receipt, err := mdm.DecodeCheckin(raw)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := a.Core.Checkin(ctx, &mdm.Request{Certificate: candidate.Cert}, receipt); err != nil {
					t.Fatal(err)
				}
				return raw
			}
			authenticate := map[string]any{"MessageType": "Authenticate", "Topic": "com.apple.mgmt.test", "SerialNumber": "SER", "OSVersion": "27.0"}
			if backend == "sqlite" {
				// The actual App callback shares the replacement transaction. A failed
				// lifecycle projection must roll back candidate Authenticate evidence.
				authenticate["UDID"] = id.ID
				failedRaw, err := plist.Marshal(authenticate)
				if err != nil {
					t.Fatal(err)
				}
				failedReceipt, err := mdm.DecodeCheckin(failedRaw)
				if err != nil {
					t.Fatal(err)
				}
				backing := a.Inventory.Backend
				a.Inventory.Backend = inventoryFailureBackend{Backend: backing, updates: true}
				_, receiptErr := a.Core.Checkin(ctx, &mdm.Request{Certificate: candidate.Cert}, failedReceipt)
				a.Inventory.Backend = backing
				if !errors.Is(receiptErr, errInventoryStorage) {
					t.Fatal("inventory outage appeared successful", receiptErr)
				}
				rolledBack, err := transitions.TransitionReplacement(ctx, id, storage.ReplacementChange{Op: "read", At: at})
				if err != nil || rolledBack.Authenticated || len(rolledBack.AuthenticateRaw) != 0 {
					t.Fatal("failed observer committed candidate authentication", rolledBack, err)
				}
			}
			raw := checkin(authenticate)
			pending, err := transitions.TransitionReplacement(ctx, id, storage.ReplacementChange{Op: "read", At: at})
			if err != nil || !pending.Authenticated || string(pending.AuthenticateRaw) != string(raw) {
				t.Fatal("accepted Authenticate did not persist", pending, err)
			}
			if _, err := a.Inventory.SourceResource(ctx, inventory.EnrollmentReference("mdm.identity", id)); !errors.Is(err, inventory.ErrNotFound) {
				t.Fatal("candidate identity projected before active pin", err)
			}
			afterCandidate, err := a.Inventory.History(ctx, certificateRef)
			if err != nil || len(afterCandidate) != len(certificateHistory) {
				t.Fatal("candidate certificate projected under old pin", afterCandidate, err)
			}
			checkin(map[string]any{"MessageType": "TokenUpdate", "Topic": "com.apple.mgmt.test", "Token": []byte("candidate-token"), "PushMagic": "candidate"})
			before, err := a.Store.Get(ctx, id)
			if err != nil || before.CertHash != oldHash {
				t.Fatal("premature pin switch", before, err)
			}
			connect(true, mdm.StatusAcknowledged, command.UUID)
			committed, err := a.Store.Get(ctx, id)
			if err != nil || committed.CertHash != newHash {
				t.Fatal("replacement did not commit", committed, err)
			}
			checkin(authenticate)
			facts, err := a.Inventory.SourceResource(ctx, inventory.EnrollmentReference("mdm.identity", id))
			if err != nil || facts.Snapshot.Native == nil || !facts.Snapshot.Native.Authenticated || facts.Snapshot.Native.Serial != "SER" || inventory.String(facts.Snapshot.Fields["OSVersion"]) != "27.0" {
				t.Fatal("fresh active-pin receipt not qualified", facts, err)
			}
		})
	}
}
