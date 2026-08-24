package store_test

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"deep-reader/internal/ports"
)

// newPasskey builds a Passkey with the given credential id and label.
func newPasskey(credID, name string) ports.Passkey {
	return ports.Passkey{
		Name:         name,
		CredentialID: []byte(credID),
		Credential:   []byte(`{"id":"` + credID + `","publicKey":"AAAA"}`),
	}
}

func TestCreateUser_AssignsWebAuthnUserHandle(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()

	if err := s.CreateUser(ctx, "reader", "hash"); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	u, err := s.GetUser(ctx)
	if err != nil {
		t.Fatalf("GetUser: %v", err)
	}
	// The handle is what a discoverable credential stores and echoes back on
	// login; a zero-length or short one would break that lookup.
	if len(u.WebAuthnUserHandle) != 32 {
		t.Fatalf("WebAuthnUserHandle length = %d, want 32", len(u.WebAuthnUserHandle))
	}

	// It must be stable: a later read returns the same bytes, or every existing
	// passkey would stop resolving.
	again, err := s.GetUser(ctx)
	if err != nil {
		t.Fatalf("GetUser (2): %v", err)
	}
	if !bytes.Equal(u.WebAuthnUserHandle, again.WebAuthnUserHandle) {
		t.Fatal("WebAuthnUserHandle changed between reads")
	}
}

func TestCreatePasskey_RoundTrip(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()

	created, err := s.CreatePasskey(ctx, newPasskey("cred-1", "MacBook"))
	if err != nil {
		t.Fatalf("CreatePasskey: %v", err)
	}
	if created.ID == "" {
		t.Error("CreatePasskey did not assign an id")
	}
	if created.CreatedAt.IsZero() {
		t.Error("CreatePasskey did not stamp CreatedAt")
	}
	if created.LastUsedAt != nil {
		t.Error("a fresh passkey must have no LastUsedAt")
	}

	got, err := s.GetPasskeyByCredentialID(ctx, []byte("cred-1"))
	if err != nil {
		t.Fatalf("GetPasskeyByCredentialID: %v", err)
	}
	if got.ID != created.ID || got.Name != "MacBook" {
		t.Errorf("round trip mismatch: %+v", got)
	}
	if !bytes.Equal(got.Credential, created.Credential) {
		t.Error("credential JSON did not round-trip")
	}
}

func TestCreatePasskey_DuplicateCredentialID(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()

	if _, err := s.CreatePasskey(ctx, newPasskey("cred-1", "MacBook")); err != nil {
		t.Fatalf("CreatePasskey: %v", err)
	}
	_, err := s.CreatePasskey(ctx, newPasskey("cred-1", "MacBook again"))
	if !errors.Is(err, ports.ErrDuplicate) {
		t.Fatalf("second CreatePasskey error = %v, want ErrDuplicate", err)
	}
}

func TestGetPasskeyByCredentialID_NotFound(t *testing.T) {
	s := openStore(t)

	_, err := s.GetPasskeyByCredentialID(context.Background(), []byte("nope"))
	if !errors.Is(err, ports.ErrNotFound) {
		t.Fatalf("error = %v, want ErrNotFound", err)
	}
}

func TestListPasskeys_NewestFirstAndNeverNil(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()

	// A nil slice would serialize as JSON null and break the typed client.
	empty, err := s.ListPasskeys(ctx)
	if err != nil {
		t.Fatalf("ListPasskeys: %v", err)
	}
	if empty == nil {
		t.Fatal("ListPasskeys returned nil, want an empty slice")
	}

	for _, name := range []string{"first", "second", "third"} {
		if _, err := s.CreatePasskey(ctx, newPasskey("cred-"+name, name)); err != nil {
			t.Fatalf("CreatePasskey(%s): %v", name, err)
		}
	}

	list, err := s.ListPasskeys(ctx)
	if err != nil {
		t.Fatalf("ListPasskeys: %v", err)
	}
	if len(list) != 3 {
		t.Fatalf("len(list) = %d, want 3", len(list))
	}
	// Same-second timestamps are broken by the ULID id, which is monotonic.
	if list[0].Name != "third" || list[2].Name != "first" {
		t.Errorf("order = %s,%s,%s; want third,second,first", list[0].Name, list[1].Name, list[2].Name)
	}
}

func TestTouchPasskey_UpdatesCredentialAndLastUsed(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()

	created, err := s.CreatePasskey(ctx, newPasskey("cred-1", "MacBook"))
	if err != nil {
		t.Fatalf("CreatePasskey: %v", err)
	}

	used := time.Now().UTC().Truncate(time.Second)
	updated := []byte(`{"id":"cred-1","publicKey":"AAAA","authenticator":{"signCount":7}}`)
	if err := s.TouchPasskey(ctx, created.ID, updated, used); err != nil {
		t.Fatalf("TouchPasskey: %v", err)
	}

	got, err := s.GetPasskeyByCredentialID(ctx, []byte("cred-1"))
	if err != nil {
		t.Fatalf("GetPasskeyByCredentialID: %v", err)
	}
	if !bytes.Equal(got.Credential, updated) {
		t.Error("TouchPasskey did not persist the updated credential")
	}
	if got.LastUsedAt == nil || !got.LastUsedAt.Equal(used) {
		t.Errorf("LastUsedAt = %v, want %v", got.LastUsedAt, used)
	}
}

func TestTouchPasskey_NotFound(t *testing.T) {
	s := openStore(t)

	err := s.TouchPasskey(context.Background(), "missing", []byte(`{}`), time.Now().UTC())
	if !errors.Is(err, ports.ErrNotFound) {
		t.Fatalf("error = %v, want ErrNotFound", err)
	}
}

func TestRenamePasskey(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()

	created, err := s.CreatePasskey(ctx, newPasskey("cred-1", "old"))
	if err != nil {
		t.Fatalf("CreatePasskey: %v", err)
	}
	if err := s.RenamePasskey(ctx, created.ID, "new"); err != nil {
		t.Fatalf("RenamePasskey: %v", err)
	}
	got, err := s.GetPasskeyByCredentialID(ctx, []byte("cred-1"))
	if err != nil {
		t.Fatalf("GetPasskeyByCredentialID: %v", err)
	}
	if got.Name != "new" {
		t.Errorf("Name = %q, want %q", got.Name, "new")
	}

	if err := s.RenamePasskey(ctx, "missing", "x"); !errors.Is(err, ports.ErrNotFound) {
		t.Fatalf("RenamePasskey(missing) = %v, want ErrNotFound", err)
	}
}

func TestDeletePasskey(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()

	created, err := s.CreatePasskey(ctx, newPasskey("cred-1", "MacBook"))
	if err != nil {
		t.Fatalf("CreatePasskey: %v", err)
	}
	if err := s.DeletePasskey(ctx, created.ID); err != nil {
		t.Fatalf("DeletePasskey: %v", err)
	}
	if _, err := s.GetPasskeyByCredentialID(ctx, []byte("cred-1")); !errors.Is(err, ports.ErrNotFound) {
		t.Fatalf("after delete = %v, want ErrNotFound", err)
	}
	if err := s.DeletePasskey(ctx, created.ID); !errors.Is(err, ports.ErrNotFound) {
		t.Fatalf("second DeletePasskey = %v, want ErrNotFound", err)
	}
}
