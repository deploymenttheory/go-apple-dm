package adminauth_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/server/adminauth"
	"github.com/deploymenttheory/go-apple-dm/server/adminauth/adminauthtest"
)

// TestRoleAdministrationRejectsUnauthorizedAndInvalidChanges checks that role administration
// rejects unauthorized and invalid changes.
func TestRoleAdministrationRejectsUnauthorizedAndInvalidChanges(t *testing.T) {
	m, store, _ := manager(t)
	ctx := t.Context()
	reader := adminauth.Principal{Name: "reader"}
	version, err := store.PolicyVersion(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.PutRole(ctx, reader, adminauth.Role{Name: "new"}); !errors.Is(err, adminauth.ErrDenied) {
		t.Fatalf("non-root role creation: %v", err)
	}
	if err := m.DeleteRole(ctx, reader, "reader"); !errors.Is(err, adminauth.ErrDenied) {
		t.Fatalf("non-root role deletion: %v", err)
	}
	if _, err := m.PutRole(ctx, adminauth.Root, adminauth.Role{Name: "invalid/name"}); !errors.Is(err, adminauth.ErrInvalid) {
		t.Fatalf("invalid role name: %v", err)
	}
	if current, err := store.PolicyVersion(ctx); err != nil || current != version {
		t.Fatalf("rejected change modified authority: %d, %v", current, err)
	}
	if _, err := m.Role(ctx, "reader"); err != nil {
		t.Fatalf("rejected deletion removed role: %v", err)
	}
}

// TestPolicyReferencesRejectMissingEntitiesAndMalformedSource checks policy references reject
// missing entities and malformed source.
func TestPolicyReferencesRejectMissingEntitiesAndMalformedSource(t *testing.T) {
	m, store, _ := manager(t)
	for _, source := range []string{
		`permit(principal in MDM::Role::"missing", action, resource);`,
		`permit(principal == MDM::Principal::"missing", action, resource);`,
		`not a Cedar policy`,
	} {
		inactive := false
		doc := adminauth.Policy{Name: "candidate", Source: source, Active: &inactive}
		if err := adminauth.ValidateReferences(t.Context(), store, doc); !errors.Is(err, adminauth.ErrInvalid) {
			t.Fatalf("invalid references accepted: %v", err)
		}
		if _, err := m.PutPolicy(t.Context(), adminauth.Root, doc); !errors.Is(err, adminauth.ErrInvalid) {
			t.Fatalf("invalid policy stored: %v", err)
		}
		if _, err := store.GetPolicy(t.Context(), doc.Name); !errors.Is(err, adminauth.ErrNotFound) {
			t.Fatalf("failed publication left a policy: %v", err)
		}
	}
}

// TestBootstrapValidationDoesNotConsumeCredential checks that bootstrap validation does not
// consume credential.
func TestBootstrapValidationDoesNotConsumeCredential(t *testing.T) {
	m, _, _ := manager(t)
	for _, tc := range []struct {
		name    string
		expires time.Time
	}{{"invalid/name", time.Time{}}, {"first", t0}, {"first", t0.Add(-time.Second)}} {
		_, token, err := m.Bootstrap(t.Context(), tc.name, tc.expires)
		if !errors.Is(err, adminauth.ErrInvalid) || token != "" {
			t.Fatalf("invalid bootstrap returned credential: %v", err)
		}
	}
	if initialized, err := m.Initialized(t.Context()); err != nil || initialized {
		t.Fatalf("invalid request consumed bootstrap: %v %v", initialized, err)
	}
	if _, token, err := m.Bootstrap(t.Context(), "first", t0.Add(time.Hour)); err != nil || token == "" {
		t.Fatalf("valid bootstrap failed after invalid requests: %v", err)
	}
}

// TestInvalidMembershipAndFailedRotationPreservePrincipal checks invalid membership and failed
// rotation preserve principal.
func TestInvalidMembershipAndFailedRotationPreservePrincipal(t *testing.T) {
	m, store, _ := manager(t)
	p, token := newRoot(t, m, "root")
	if _, err := m.UpdatePrincipal(t.Context(), p, p.Name, []string{"bad/name"}, true); !errors.Is(err, adminauth.ErrInvalid) {
		t.Fatalf("invalid role update: %v", err)
	}
	if _, err := store.ApplyPrincipal(t.Context(), p.Name, adminauth.PrincipalChange{Op: "update", Roles: []string{"bad/name"}}, t0); !errors.Is(err, adminauth.ErrInvalid) {
		t.Fatalf("invalid role change: %v", err)
	}
	broken, err := adminauth.New(&adminauthtest.Failing{Store: store, Fail: "SetToken"}, registry(t))
	if err != nil {
		t.Fatal(err)
	}
	if _, replacement, err := broken.Rotate(t.Context(), p, p.Name, time.Time{}); !errors.Is(err, adminauthtest.ErrFailing) || replacement != "" {
		t.Fatalf("failed rotation exposed credential: %v", err)
	}
	if got, err := m.Authenticate(t.Context(), token); err != nil || got.Name != p.Name {
		t.Fatalf("failed change invalidated old credential: %+v, %v", got, err)
	}
}

type changingPolicyVersion struct {
	adminauth.Store
	reads  int
	stopAt int
	failAt int
}

// PolicyVersion changes the observed policy version across reads and optionally fails the
// configured read.
func (s *changingPolicyVersion) PolicyVersion(ctx context.Context) (int64, error) {
	s.reads++
	if s.reads == s.failAt {
		return 0, adminauthtest.ErrFailing
	}
	v, err := s.Store.PolicyVersion(ctx)
	return v + int64(min(s.reads, s.stopAt)), err
}

// TestPolicyCompilationRequiresConsistentAuthority checks that policy compilation requires
// consistent authority.
func TestPolicyCompilationRequiresConsistentAuthority(t *testing.T) {
	for _, tc := range []struct {
		name           string
		stopAt, failAt int
		want           error
	}{{"eventually-stable", 2, 0, nil}, {"keeps-changing", 100, 0, adminauth.ErrInvalid}, {"second-read-fails", 100, 2, adminauthtest.ErrFailing}} {
		t.Run(tc.name, func(t *testing.T) {
			_, base, _ := manager(t)
			store := &changingPolicyVersion{Store: base, stopAt: tc.stopAt, failAt: tc.failAt}
			m, err := adminauth.New(store, registry(t))
			if err != nil {
				t.Fatal(err)
			}
			decision, err := m.Authorize(t.Context(), adminauth.Root, "listEnrollments", adminauth.SystemResource, nil)
			if !errors.Is(err, tc.want) || decision.Allowed {
				t.Fatalf("inconsistent or empty policies allowed request: %+v, %v", decision, err)
			}
			if store.reads > 6 {
				t.Fatalf("unbounded retry: %d reads", store.reads)
			}
		})
	}
}
