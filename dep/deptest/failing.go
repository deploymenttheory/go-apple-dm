package deptest

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/deploymenttheory/go-apple-mdm/dep"
	"github.com/deploymenttheory/go-apple-mdm/storage"
)

// Failing wraps a dep.Store and returns the error in Fail for any method
// named there, inside Update too, so error paths of the client, syncer,
// and assigner can be exercised without a broken database.
type Failing struct {
	Store dep.Store
	// Fail maps a method name (PutDevices, SetCursor, ...) to the error it
	// returns. Assign it before the store is in use; a test that turns a
	// fault on or off while a server is serving must call SetFail, or the
	// write races the handler goroutine reading it.
	Fail map[string]error
	// After lets a method fail only from the Nth call on (1 fails at
	// once); calls are counted per method.
	After map[string]int

	// mu guards Fail and calls against a handler goroutine reading them
	// while a test changes them.
	mu    sync.Mutex
	calls map[string]int
}

var _ dep.Store = (*Failing)(nil)

// SetFail replaces the failure map while the store may be in use.
func (f *Failing) SetFail(fail map[string]error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Fail = fail
}

func (f *Failing) fail(method string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	err, ok := f.Fail[method]
	if !ok {
		return nil
	}
	if f.calls == nil {
		f.calls = map[string]int{}
	}
	f.calls[method]++
	if after, ok := f.After[method]; ok && f.calls[method] < after {
		return nil
	}
	return fmt.Errorf("deptest: %s: %w", method, err)
}

// txView is the transaction the callback receives.
type txView struct {
	f  *Failing
	tx dep.Tx
}

// Update implements dep.Store: fn runs inside the wrapped store's
// transaction against a wrapped Tx.
func (f *Failing) Update(ctx context.Context, fn func(dep.Tx) error) error {
	if err := f.fail("Update"); err != nil {
		return err
	}
	return f.Store.Update(ctx, func(tx dep.Tx) error { return fn(&txView{f: f, tx: tx}) })
}

// PutAccount implements dep.AccountStore.
func (f *Failing) PutAccount(ctx context.Context, a *dep.Account) error {
	if err := f.fail("PutAccount"); err != nil {
		return err
	}
	return f.Store.PutAccount(ctx, a)
}

// GetAccount implements dep.AccountStore.
func (f *Failing) GetAccount(ctx context.Context, name string) (*dep.Account, error) {
	if err := f.fail("GetAccount"); err != nil {
		return nil, err
	}
	return f.Store.GetAccount(ctx, name)
}

// DeleteAccount implements dep.AccountStore.
func (f *Failing) DeleteAccount(ctx context.Context, name string) error {
	if err := f.fail("DeleteAccount"); err != nil {
		return err
	}
	return f.Store.DeleteAccount(ctx, name)
}

// ListAccounts implements dep.AccountStore.
func (f *Failing) ListAccounts(ctx context.Context, p storage.Page) (storage.Result[dep.Account], error) {
	if err := f.fail("ListAccounts"); err != nil {
		return storage.Result[dep.Account]{}, err
	}
	return f.Store.ListAccounts(ctx, p)
}

// SetAccountState implements dep.AccountStore.
func (f *Failing) SetAccountState(ctx context.Context, name string, s dep.AccountState) error {
	if err := f.fail("SetAccountState"); err != nil {
		return err
	}
	return f.Store.SetAccountState(ctx, name, s)
}

// PutKeypair implements dep.AccountStore.
func (f *Failing) PutKeypair(ctx context.Context, name string, stage dep.Stage, kp *dep.Keypair) error {
	if err := f.fail("PutKeypair"); err != nil {
		return err
	}
	return f.Store.PutKeypair(ctx, name, stage, kp)
}

// Keypair implements dep.AccountStore.
func (f *Failing) Keypair(ctx context.Context, name string, stage dep.Stage) (*dep.Keypair, error) {
	if err := f.fail("Keypair"); err != nil {
		return nil, err
	}
	return f.Store.Keypair(ctx, name, stage)
}

// UpstageKeypair implements dep.AccountStore.
func (f *Failing) UpstageKeypair(ctx context.Context, name string) error {
	if err := f.fail("UpstageKeypair"); err != nil {
		return err
	}
	return f.Store.UpstageKeypair(ctx, name)
}

// Session implements dep.SessionStore.
func (f *Failing) Session(ctx context.Context, name string) (string, error) {
	if err := f.fail("Session"); err != nil {
		return "", err
	}
	return f.Store.Session(ctx, name)
}

// SetSession implements dep.SessionStore.
func (f *Failing) SetSession(ctx context.Context, name, token string) error {
	if err := f.fail("SetSession"); err != nil {
		return err
	}
	return f.Store.SetSession(ctx, name, token)
}

// Cursor implements dep.CursorStore.
func (f *Failing) Cursor(ctx context.Context, name string) (dep.Cursor, error) {
	if err := f.fail("Cursor"); err != nil {
		return dep.Cursor{}, err
	}
	return f.Store.Cursor(ctx, name)
}

// SetCursor implements dep.CursorStore.
func (f *Failing) SetCursor(ctx context.Context, name string, c dep.Cursor) error {
	if err := f.fail("SetCursor"); err != nil {
		return err
	}
	return f.Store.SetCursor(ctx, name, c)
}

// PutDevices implements dep.DeviceStore.
func (f *Failing) PutDevices(ctx context.Context, account string, devs []dep.Device, at time.Time) error {
	if err := f.fail("PutDevices"); err != nil {
		return err
	}
	return f.Store.PutDevices(ctx, account, devs, at)
}

// GetDevice implements dep.DeviceStore.
func (f *Failing) GetDevice(ctx context.Context, account, serial string) (*dep.StoredDevice, error) {
	if err := f.fail("GetDevice"); err != nil {
		return nil, err
	}
	return f.Store.GetDevice(ctx, account, serial)
}

// ListDevices implements dep.DeviceStore.
func (f *Failing) ListDevices(ctx context.Context, account string, q dep.DeviceQuery, p storage.Page) (storage.Result[dep.StoredDevice], error) {
	if err := f.fail("ListDevices"); err != nil {
		return storage.Result[dep.StoredDevice]{}, err
	}
	return f.Store.ListDevices(ctx, account, q, p)
}

// PutProfile implements dep.ProfileStore.
func (f *Failing) PutProfile(ctx context.Context, account string, p *dep.Profile) error {
	if err := f.fail("PutProfile"); err != nil {
		return err
	}
	return f.Store.PutProfile(ctx, account, p)
}

// GetProfile implements dep.ProfileStore.
func (f *Failing) GetProfile(ctx context.Context, account, uuid string) (*dep.Profile, error) {
	if err := f.fail("GetProfile"); err != nil {
		return nil, err
	}
	return f.Store.GetProfile(ctx, account, uuid)
}

// DeleteProfile implements dep.ProfileStore.
func (f *Failing) DeleteProfile(ctx context.Context, account, uuid string) error {
	if err := f.fail("DeleteProfile"); err != nil {
		return err
	}
	return f.Store.DeleteProfile(ctx, account, uuid)
}

// ListProfiles implements dep.ProfileStore.
func (f *Failing) ListProfiles(ctx context.Context, account string, p storage.Page) (storage.Result[dep.Profile], error) {
	if err := f.fail("ListProfiles"); err != nil {
		return storage.Result[dep.Profile]{}, err
	}
	return f.Store.ListProfiles(ctx, account, p)
}

// PutAssignment implements dep.AssignmentStore.
func (f *Failing) PutAssignment(ctx context.Context, a *dep.Assignment) error {
	if err := f.fail("PutAssignment"); err != nil {
		return err
	}
	return f.Store.PutAssignment(ctx, a)
}

// GetAssignment implements dep.AssignmentStore.
func (f *Failing) GetAssignment(ctx context.Context, account, serial string) (*dep.Assignment, error) {
	if err := f.fail("GetAssignment"); err != nil {
		return nil, err
	}
	return f.Store.GetAssignment(ctx, account, serial)
}

// ListAssignments implements dep.AssignmentStore.
func (f *Failing) ListAssignments(ctx context.Context, account string, q dep.AssignmentQuery, p storage.Page) (storage.Result[dep.Assignment], error) {
	if err := f.fail("ListAssignments"); err != nil {
		return storage.Result[dep.Assignment]{}, err
	}
	return f.Store.ListAssignments(ctx, account, q, p)
}

// The transaction view applies the same failures inside Update.

func (t *txView) PutAccount(ctx context.Context, a *dep.Account) error {
	if err := t.f.fail("PutAccount"); err != nil {
		return err
	}
	return t.tx.PutAccount(ctx, a)
}

func (t *txView) GetAccount(ctx context.Context, name string) (*dep.Account, error) {
	if err := t.f.fail("GetAccount"); err != nil {
		return nil, err
	}
	return t.tx.GetAccount(ctx, name)
}

func (t *txView) DeleteAccount(ctx context.Context, name string) error {
	if err := t.f.fail("DeleteAccount"); err != nil {
		return err
	}
	return t.tx.DeleteAccount(ctx, name)
}

func (t *txView) ListAccounts(ctx context.Context, p storage.Page) (storage.Result[dep.Account], error) {
	if err := t.f.fail("ListAccounts"); err != nil {
		return storage.Result[dep.Account]{}, err
	}
	return t.tx.ListAccounts(ctx, p)
}

func (t *txView) SetAccountState(ctx context.Context, name string, s dep.AccountState) error {
	if err := t.f.fail("SetAccountState"); err != nil {
		return err
	}
	return t.tx.SetAccountState(ctx, name, s)
}

func (t *txView) PutKeypair(ctx context.Context, name string, stage dep.Stage, kp *dep.Keypair) error {
	if err := t.f.fail("PutKeypair"); err != nil {
		return err
	}
	return t.tx.PutKeypair(ctx, name, stage, kp)
}

func (t *txView) Keypair(ctx context.Context, name string, stage dep.Stage) (*dep.Keypair, error) {
	if err := t.f.fail("Keypair"); err != nil {
		return nil, err
	}
	return t.tx.Keypair(ctx, name, stage)
}

func (t *txView) UpstageKeypair(ctx context.Context, name string) error {
	if err := t.f.fail("UpstageKeypair"); err != nil {
		return err
	}
	return t.tx.UpstageKeypair(ctx, name)
}

func (t *txView) Session(ctx context.Context, name string) (string, error) {
	if err := t.f.fail("Session"); err != nil {
		return "", err
	}
	return t.tx.Session(ctx, name)
}

func (t *txView) SetSession(ctx context.Context, name, token string) error {
	if err := t.f.fail("SetSession"); err != nil {
		return err
	}
	return t.tx.SetSession(ctx, name, token)
}

func (t *txView) Cursor(ctx context.Context, name string) (dep.Cursor, error) {
	if err := t.f.fail("Cursor"); err != nil {
		return dep.Cursor{}, err
	}
	return t.tx.Cursor(ctx, name)
}

func (t *txView) SetCursor(ctx context.Context, name string, c dep.Cursor) error {
	if err := t.f.fail("SetCursor"); err != nil {
		return err
	}
	return t.tx.SetCursor(ctx, name, c)
}

func (t *txView) PutDevices(ctx context.Context, account string, devs []dep.Device, at time.Time) error {
	if err := t.f.fail("PutDevices"); err != nil {
		return err
	}
	return t.tx.PutDevices(ctx, account, devs, at)
}

func (t *txView) GetDevice(ctx context.Context, account, serial string) (*dep.StoredDevice, error) {
	if err := t.f.fail("GetDevice"); err != nil {
		return nil, err
	}
	return t.tx.GetDevice(ctx, account, serial)
}

func (t *txView) ListDevices(ctx context.Context, account string, q dep.DeviceQuery, p storage.Page) (storage.Result[dep.StoredDevice], error) {
	if err := t.f.fail("ListDevices"); err != nil {
		return storage.Result[dep.StoredDevice]{}, err
	}
	return t.tx.ListDevices(ctx, account, q, p)
}

func (t *txView) PutProfile(ctx context.Context, account string, p *dep.Profile) error {
	if err := t.f.fail("PutProfile"); err != nil {
		return err
	}
	return t.tx.PutProfile(ctx, account, p)
}

func (t *txView) GetProfile(ctx context.Context, account, uuid string) (*dep.Profile, error) {
	if err := t.f.fail("GetProfile"); err != nil {
		return nil, err
	}
	return t.tx.GetProfile(ctx, account, uuid)
}

func (t *txView) DeleteProfile(ctx context.Context, account, uuid string) error {
	if err := t.f.fail("DeleteProfile"); err != nil {
		return err
	}
	return t.tx.DeleteProfile(ctx, account, uuid)
}

func (t *txView) ListProfiles(ctx context.Context, account string, p storage.Page) (storage.Result[dep.Profile], error) {
	if err := t.f.fail("ListProfiles"); err != nil {
		return storage.Result[dep.Profile]{}, err
	}
	return t.tx.ListProfiles(ctx, account, p)
}

func (t *txView) PutAssignment(ctx context.Context, a *dep.Assignment) error {
	if err := t.f.fail("PutAssignment"); err != nil {
		return err
	}
	return t.tx.PutAssignment(ctx, a)
}

func (t *txView) GetAssignment(ctx context.Context, account, serial string) (*dep.Assignment, error) {
	if err := t.f.fail("GetAssignment"); err != nil {
		return nil, err
	}
	return t.tx.GetAssignment(ctx, account, serial)
}

func (t *txView) ListAssignments(ctx context.Context, account string, q dep.AssignmentQuery, p storage.Page) (storage.Result[dep.Assignment], error) {
	if err := t.f.fail("ListAssignments"); err != nil {
		return storage.Result[dep.Assignment]{}, err
	}
	return t.tx.ListAssignments(ctx, account, q, p)
}
