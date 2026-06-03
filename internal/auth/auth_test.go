package auth

import "testing"

func TestHashAndVerifyPassword(t *testing.T) {
	const pw = "correct horse battery staple"

	hash, err := HashPassword(pw)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if hash == pw {
		t.Fatal("hash must not equal the plaintext password")
	}
	if !VerifyPassword(hash, pw) {
		t.Error("VerifyPassword rejected the correct password")
	}
	if VerifyPassword(hash, "wrong password") {
		t.Error("VerifyPassword accepted a wrong password")
	}
	if VerifyPassword("not-a-bcrypt-hash", pw) {
		t.Error("VerifyPassword accepted a malformed hash")
	}
}

func TestNewSessionTokenIsUniqueAndHashes(t *testing.T) {
	a, err := NewSessionToken()
	if err != nil {
		t.Fatalf("NewSessionToken: %v", err)
	}
	b, err := NewSessionToken()
	if err != nil {
		t.Fatalf("NewSessionToken: %v", err)
	}
	if a == "" || b == "" {
		t.Fatal("session tokens must be non-empty")
	}
	if a == b {
		t.Fatal("two session tokens must differ")
	}

	// HashToken is deterministic and never returns the plaintext.
	hashA, hashA2, hashB := HashToken(a), HashToken(a), HashToken(b)
	if hashA != hashA2 {
		t.Error("HashToken must be deterministic")
	}
	if hashA == a {
		t.Error("HashToken must not return the plaintext token")
	}
	if hashA == hashB {
		t.Error("distinct tokens must hash to distinct values")
	}
}
