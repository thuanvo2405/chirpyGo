package auth

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestValidateJWT_Equal(t *testing.T) {
	secret := "my-secret-key"
	expectedID := uuid.New()

	tokenStr, err := MakeJWT(expectedID, secret, time.Hour)
	if err != nil {
		t.Fatalf("Can't create token: %v", err)
	}

	actualID, err := ValidateJWT(tokenStr, secret)
	if err != nil {
		t.Fatalf("Failed authen token: %v", err)
	}

	if actualID != expectedID {
		t.Errorf("ID expected: %v, actual receiver: %v", expectedID, actualID)
	}
}
