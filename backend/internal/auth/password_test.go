package auth

import "testing"

func TestHashAndCheckPassword(t *testing.T) {
	password := "correct-horse-battery-staple"

	hash, err := HashPassword(password)
	if err != nil {
		t.Fatalf("HashPassword() error = %v", err)
	}

	if !CheckPassword(hash, password) {
		t.Error("CheckPassword() returned false for correct password")
	}
}

func TestCheckPasswordWrong(t *testing.T) {
	hash, err := HashPassword("my-secret-password")
	if err != nil {
		t.Fatalf("HashPassword() error = %v", err)
	}

	if CheckPassword(hash, "wrong-password") {
		t.Error("CheckPassword() returned true for wrong password")
	}
}

func TestHashPasswordDifferentSalts(t *testing.T) {
	password := "same-password"

	hash1, err := HashPassword(password)
	if err != nil {
		t.Fatalf("HashPassword() first call error = %v", err)
	}

	hash2, err := HashPassword(password)
	if err != nil {
		t.Fatalf("HashPassword() second call error = %v", err)
	}

	if hash1 == hash2 {
		t.Error("two hashes of the same password should differ due to random salt")
	}
}

func TestEmptyPassword(t *testing.T) {
	hash, err := HashPassword("")
	if err != nil {
		t.Fatalf("HashPassword() error = %v", err)
	}

	if !CheckPassword(hash, "") {
		t.Error("CheckPassword() returned false for empty password")
	}

	if CheckPassword(hash, "not-empty") {
		t.Error("CheckPassword() returned true for non-empty password against empty hash")
	}
}
