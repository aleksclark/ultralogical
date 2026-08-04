package config_test

import (
	"errors"
	"os"
	"testing"

	"github.com/aleksclark/ultracore/config"
)

func TestRefuseUnknown(t *testing.T) {
	t.Setenv("CORE_ADDR", ":8080")
	if err := config.RefuseUnknown(); err != nil {
		t.Fatalf("known vars refused: %v", err)
	}
	t.Setenv("CORE_NOT_A_REAL_VAR", "1")
	err := config.RefuseUnknown()
	if err == nil {
		t.Fatal("expected unknown CORE_* to refuse startup")
	}
	var u *config.ErrUnknownEnv
	if !errors.As(err, &u) {
		t.Fatalf("want ErrUnknownEnv, got %T %v", err, err)
	}
	found := false
	for _, n := range u.Names {
		if n == "CORE_NOT_A_REAL_VAR" {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing name in error: %v", u.Names)
	}
	// cleanup for other tests in package
	_ = os.Unsetenv("CORE_NOT_A_REAL_VAR")
}

func TestKnownNamesNonEmpty(t *testing.T) {
	if len(config.KnownNames()) == 0 {
		t.Fatal("known names empty")
	}
}
