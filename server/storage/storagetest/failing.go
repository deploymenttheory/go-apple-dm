package storagetest

import (
	"context"
	"time"

	"github.com/deploymenttheory/go-apple-dm/v3/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/v3/paging"
	"github.com/deploymenttheory/go-apple-dm/v3/schema/checkin"
	"github.com/deploymenttheory/go-apple-dm/v3/server/storage"
)

// Failing wraps a Store and returns the configured error from any method
// whose name is in Fail, so callers can test their error paths.
type Failing struct {
	storage.Store
	Fail map[string]error
}

func (f *Failing) fail(method string) error {
	if f.Fail == nil {
		return nil
	}
	return f.Fail[method]
}

// UpsertAuthenticate implements storage.EnrollmentStore.
func (f *Failing) UpsertAuthenticate(ctx context.Context, id mdm.EnrollmentID, msg *checkin.Authenticate, raw []byte, at time.Time) error {
	if err := f.fail("UpsertAuthenticate"); err != nil {
		return err
	}
	return f.Store.UpsertAuthenticate(ctx, id, msg, raw, at)
}

// StoreTokenUpdate implements storage.EnrollmentStore.
func (f *Failing) StoreTokenUpdate(ctx context.Context, id mdm.EnrollmentID, push mdm.Push, msg *checkin.TokenUpdate, raw []byte, at time.Time) error {
	if err := f.fail("StoreTokenUpdate"); err != nil {
		return err
	}
	return f.Store.StoreTokenUpdate(ctx, id, push, msg, raw, at)
}

// Disable implements storage.EnrollmentStore.
func (f *Failing) Disable(ctx context.Context, id mdm.EnrollmentID, at time.Time) error {
	if err := f.fail("Disable"); err != nil {
		return err
	}
	return f.Store.Disable(ctx, id, at)
}

// Get implements storage.EnrollmentStore.
func (f *Failing) Get(ctx context.Context, id mdm.EnrollmentID) (*storage.Enrollment, error) {
	if err := f.fail("Get"); err != nil {
		return nil, err
	}
	return f.Store.Get(ctx, id)
}

// List implements storage.EnrollmentStore.
func (f *Failing) List(ctx context.Context, q storage.EnrollmentQuery, p paging.Page) (paging.Result[storage.Enrollment], error) {
	if err := f.fail("List"); err != nil {
		return paging.Result[storage.Enrollment]{}, err
	}
	return f.Store.List(ctx, q, p)
}

// TouchLastSeen implements storage.EnrollmentStore.
func (f *Failing) TouchLastSeen(ctx context.Context, id mdm.EnrollmentID, at time.Time) error {
	if err := f.fail("TouchLastSeen"); err != nil {
		return err
	}
	return f.Store.TouchLastSeen(ctx, id, at)
}

// Enqueue implements storage.CommandQueue.
func (f *Failing) Enqueue(ctx context.Context, ids []mdm.EnrollmentID, cmd *mdm.Command, o storage.EnqueueOptions) (storage.EnqueueResult, error) {
	if err := f.fail("Enqueue"); err != nil {
		return storage.EnqueueResult{}, err
	}
	return f.Store.Enqueue(ctx, ids, cmd, o)
}

// Next implements storage.CommandQueue.
func (f *Failing) Next(ctx context.Context, id mdm.EnrollmentID, skipNotNow bool, now time.Time) (*mdm.Command, error) {
	if err := f.fail("Next"); err != nil {
		return nil, err
	}
	return f.Store.Next(ctx, id, skipNotNow, now)
}

// StoreResult implements storage.CommandQueue.
func (f *Failing) StoreResult(ctx context.Context, id mdm.EnrollmentID, resp *mdm.Response, now time.Time) error {
	if err := f.fail("StoreResult"); err != nil {
		return err
	}
	return f.Store.StoreResult(ctx, id, resp, now)
}

// Commands implements storage.CommandQueue.
func (f *Failing) Commands(ctx context.Context, id mdm.EnrollmentID, q storage.CommandQuery, p paging.Page) (paging.Result[storage.QueuedCommand], error) {
	if err := f.fail("Commands"); err != nil {
		return paging.Result[storage.QueuedCommand]{}, err
	}
	return f.Store.Commands(ctx, id, q, p)
}

// Clear implements storage.CommandQueue.
func (f *Failing) Clear(ctx context.Context, id mdm.EnrollmentID, filter storage.ClearFilter) (int64, error) {
	if err := f.fail("Clear"); err != nil {
		return 0, err
	}
	return f.Store.Clear(ctx, id, filter)
}

// PushInfo implements storage.PushStore.
func (f *Failing) PushInfo(ctx context.Context, ids []mdm.EnrollmentID) (map[mdm.EnrollmentID]mdm.Push, error) {
	if err := f.fail("PushInfo"); err != nil {
		return nil, err
	}
	return f.Store.PushInfo(ctx, ids)
}

// AssociateCert implements storage.CertAuthStore.
func (f *Failing) AssociateCert(ctx context.Context, id mdm.EnrollmentID, hash string, at time.Time) error {
	if err := f.fail("AssociateCert"); err != nil {
		return err
	}
	return f.Store.AssociateCert(ctx, id, hash, at)
}

// CertHash implements storage.CertAuthStore.
func (f *Failing) CertHash(ctx context.Context, id mdm.EnrollmentID) (string, error) {
	if err := f.fail("CertHash"); err != nil {
		return "", err
	}
	return f.Store.CertHash(ctx, id)
}

// EnrollmentByCertHash implements storage.CertAuthStore.
func (f *Failing) EnrollmentByCertHash(ctx context.Context, hash string) (mdm.EnrollmentID, error) {
	if err := f.fail("EnrollmentByCertHash"); err != nil {
		return mdm.EnrollmentID{}, err
	}
	return f.Store.EnrollmentByCertHash(ctx, hash)
}

// CertHistory implements storage.CertAuthStore.
func (f *Failing) CertHistory(ctx context.Context, id mdm.EnrollmentID) ([]storage.CertAssociation, error) {
	if err := f.fail("CertHistory"); err != nil {
		return nil, err
	}
	return f.Store.CertHistory(ctx, id)
}

// CertHashHistory implements storage.CertAuthStore.
func (f *Failing) CertHashHistory(ctx context.Context, hash string) ([]storage.CertAssociation, error) {
	if err := f.fail("CertHashHistory"); err != nil {
		return nil, err
	}
	return f.Store.CertHashHistory(ctx, hash)
}

// StoreBootstrapToken implements storage.BootstrapTokenStore.
func (f *Failing) StoreBootstrapToken(ctx context.Context, id mdm.EnrollmentID, token []byte, at time.Time) error {
	if err := f.fail("StoreBootstrapToken"); err != nil {
		return err
	}
	return f.Store.StoreBootstrapToken(ctx, id, token, at)
}

// BootstrapToken implements storage.BootstrapTokenStore.
func (f *Failing) BootstrapToken(ctx context.Context, id mdm.EnrollmentID) ([]byte, error) {
	if err := f.fail("BootstrapToken"); err != nil {
		return nil, err
	}
	return f.Store.BootstrapToken(ctx, id)
}

// StorePushCert implements storage.PushCertStore.
func (f *Failing) StorePushCert(ctx context.Context, topic string, certPEM, keyPEM []byte, at time.Time) (storage.PushCert, error) {
	if err := f.fail("StorePushCert"); err != nil {
		return storage.PushCert{}, err
	}
	return f.Store.StorePushCert(ctx, topic, certPEM, keyPEM, at)
}

// PushCert implements storage.PushCertStore.
func (f *Failing) PushCert(ctx context.Context, topic string) (*storage.PushCert, error) {
	if err := f.fail("PushCert"); err != nil {
		return nil, err
	}
	return f.Store.PushCert(ctx, topic)
}

// PushCerts implements storage.PushCertStore.
func (f *Failing) PushCerts(ctx context.Context) ([]storage.PushCert, error) {
	if err := f.fail("PushCerts"); err != nil {
		return nil, err
	}
	return f.Store.PushCerts(ctx)
}

// PushCertVersion implements storage.PushCertStore.
func (f *Failing) PushCertVersion(ctx context.Context, topic string) (int64, error) {
	if err := f.fail("PushCertVersion"); err != nil {
		return 0, err
	}
	return f.Store.PushCertVersion(ctx, topic)
}

// StoreUserAuthChallenge implements storage.UserAuthStore.
func (f *Failing) StoreUserAuthChallenge(ctx context.Context, id mdm.EnrollmentID, challenge string, raw []byte, at time.Time) error {
	if err := f.fail("StoreUserAuthChallenge"); err != nil {
		return err
	}
	return f.Store.StoreUserAuthChallenge(ctx, id, challenge, raw, at)
}

// StoreUserAuthToken implements storage.UserAuthStore.
func (f *Failing) StoreUserAuthToken(ctx context.Context, id mdm.EnrollmentID, token string, raw []byte, at time.Time) error {
	if err := f.fail("StoreUserAuthToken"); err != nil {
		return err
	}
	return f.Store.StoreUserAuthToken(ctx, id, token, raw, at)
}

// UserAuth implements storage.UserAuthStore.
func (f *Failing) UserAuth(ctx context.Context, id mdm.EnrollmentID) (*storage.UserAuthState, error) {
	if err := f.fail("UserAuth"); err != nil {
		return nil, err
	}
	return f.Store.UserAuth(ctx, id)
}

// ClearUserAuth implements storage.UserAuthStore.
func (f *Failing) ClearUserAuth(ctx context.Context, id mdm.EnrollmentID) error {
	if err := f.fail("ClearUserAuth"); err != nil {
		return err
	}
	return f.Store.ClearUserAuth(ctx, id)
}

// Export implements storage.MigrationStore.
func (f *Failing) Export(ctx context.Context, p paging.Page) (paging.Result[storage.EnrollmentExport], error) {
	if err := f.fail("Export"); err != nil {
		return paging.Result[storage.EnrollmentExport]{}, err
	}
	return f.Store.Export(ctx, p)
}

// Import implements storage.MigrationStore.
func (f *Failing) Import(ctx context.Context, rec storage.EnrollmentExport) error {
	if err := f.fail("Import"); err != nil {
		return err
	}
	return f.Store.Import(ctx, rec)
}
