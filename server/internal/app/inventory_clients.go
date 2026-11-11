package app

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"reflect"
	"sync"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/appleplatformservices/axm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/inventory"
)

const inventoryClientLimit = 128

type inventoryClientIdentity struct {
	account, client, keyID, scope, baseURL, tokenURL, rootCA string
	revision, created                                        int64
	key                                                      [sha256.Size]byte
}
type inventoryClientEntry struct {
	identity   inventoryClientIdentity
	httpClient *http.Client
	transport  http.RoundTripper
	client     *axm.Client
	used       uint64
}
type inventoryClientPool struct {
	mu       sync.Mutex
	entries  map[string]inventoryClientEntry
	sequence uint64
}

// sameTransport requires a stable comparable identity; function transports remain uncached.
func sameTransport(left, right http.RoundTripper) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	typ := reflect.TypeOf(left)
	return typ == reflect.TypeOf(right) && typ.Comparable() && left == right
}

// reuseInventoryClient shares the SDK's synchronized token cache only for the same
// account incarnation, revision, credential bytes, endpoints and outbound transport.
// The bounded pool never changes the account snapshots or result fences held by callers.
func (a *App) reuseInventoryClient(ctx context.Context, account inventory.Account, key []byte) (*axm.Client, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("AXM client context: %w", err)
	}
	identity := inventoryClientIdentity{account: account.ID, client: account.ClientID, keyID: account.KeyID, scope: account.Scope, baseURL: account.BaseURL, tokenURL: account.TokenURL, rootCA: a.cfg.AxM.RootCAFile, revision: account.Revision, created: account.CreatedAt.UnixNano(), key: sha256.Sum256(key)}
	var transport http.RoundTripper
	if a.cfg.AxM.HTTPClient != nil {
		transport = a.cfg.AxM.HTTPClient.Transport
	}
	pool := &a.inventoryClients
	pool.mu.Lock()
	defer pool.mu.Unlock()
	pool.sequence++
	if entry, ok := pool.entries[account.ID]; ok && entry.identity == identity && entry.httpClient == a.cfg.AxM.HTTPClient && sameTransport(entry.transport, transport) {
		entry.used = pool.sequence
		pool.entries[account.ID] = entry
		return entry.client, nil
	}
	outbound, err := outboundClient(a.cfg.AxM.HTTPClient, a.cfg.AxM.RootCAFile)
	if err != nil {
		return nil, err
	}
	client, err := axm.New(ctx, axm.Config{ClientID: account.ClientID, KeyID: account.KeyID, PrivateKeyPEM: key, Scope: account.Scope, BaseURL: account.BaseURL, TokenURL: account.TokenURL, HTTPClient: outbound, Logger: a.cfg.Logger, PageCap: 100000})
	if err != nil {
		return nil, err
	}
	if !sameTransport(transport, transport) {
		return client, nil
	}
	if pool.entries == nil {
		pool.entries = make(map[string]inventoryClientEntry)
	}
	if _, exists := pool.entries[account.ID]; !exists && len(pool.entries) >= inventoryClientLimit {
		oldest := ""
		var used uint64
		for id, entry := range pool.entries {
			if oldest == "" || entry.used < used {
				oldest, used = id, entry.used
			}
		}
		delete(pool.entries, oldest)
	}
	pool.entries[account.ID] = inventoryClientEntry{identity: identity, httpClient: a.cfg.AxM.HTTPClient, transport: transport, client: client, used: pool.sequence}
	return client, nil
}
