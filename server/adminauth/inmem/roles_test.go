package inmem_test

import (
	"errors"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/server/adminauth"
	"github.com/deploymenttheory/go-apple-dm/server/adminauth/inmem"
)

func TestRoleValidationPreservesAuthority(t *testing.T) {
	s := inmem.New()
	ctx, now := t.Context(), time.Now().UTC()
	if _, err := s.PutRole(ctx, adminauth.Role{Name: "invalid/name"}, now); !errors.Is(err, adminauth.ErrInvalid) {
		t.Fatalf("invalid role stored: %v", err)
	}
	for _, source := range []string{`invalid policy`, `permit(principal in MDM::Role::"missing",action,resource);`, `permit(principal == MDM::Principal::"missing",action,resource);`} {
		if _, err := s.PutPolicy(ctx, adminauth.Policy{Name: "invalid", Source: source}, now); !errors.Is(err, adminauth.ErrInvalid) {
			t.Fatalf("invalid policy stored: %v", err)
		}
	}
	if _, err := s.PutRole(ctx, adminauth.Role{Name: "reader"}, now); err != nil {
		t.Fatal(err)
	}
	p, err := s.CreatePrincipal(ctx, adminauth.Principal{Name: "operator", Roles: []string{"reader"}}, "digest", now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdatePrincipal(ctx, p.Name, []string{"missing"}, false, now); !errors.Is(err, adminauth.ErrInvalid) {
		t.Fatalf("unknown membership accepted: %v", err)
	}
	if got, err := s.Principal(ctx, p.Name); err != nil || len(got.Roles) != 1 || got.Roles[0] != "reader" {
		t.Fatalf("invalid update changed membership: %+v %v", got, err)
	}
	if err := s.DeleteRole(ctx, "missing"); !errors.Is(err, adminauth.ErrNotFound) {
		t.Fatalf("unknown role deletion: %v", err)
	}
	if page, err := s.Roles(ctx, adminauth.Page{}); err != nil || len(page.Items) != 1 || page.Items[0].Name != "reader" {
		t.Fatalf("default role listing: %+v %v", page, err)
	}
}

func TestInvalidBootstrapDoesNotInitializeStore(t *testing.T) {
	s := inmem.New()
	ctx, now := t.Context(), time.Now().UTC()
	for _, p := range []adminauth.Principal{
		{Name: "root", TokenID: "token"},
		{Name: "root", Root: true},
		{Name: "root", Root: true, TokenID: "token", ExpiresAt: now},
	} {
		if _, err := s.BootstrapPrincipal(ctx, p, "digest", now); !errors.Is(err, adminauth.ErrInvalid) {
			t.Fatalf("invalid bootstrap accepted: %v", err)
		}
		if initialized, err := s.Initialized(ctx); err != nil || initialized {
			t.Fatalf("invalid bootstrap initialized store: %v %v", initialized, err)
		}
	}
	if _, err := s.BootstrapPrincipal(ctx, adminauth.Principal{Name: "root", Root: true, TokenID: "token"}, "digest", now); err != nil {
		t.Fatalf("failed attempts consumed bootstrap: %v", err)
	}
}
