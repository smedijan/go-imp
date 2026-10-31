package returns_test

import (
	"errors"
	"testing"

	"example.test/returns"
)

func TestAssertedFlag(t *testing.T) {
	for _, flag := range []bool{true, false} {
		if returns.AssertedFlag(flag) != flag {
			t.Fatalf("flag %v not preserved", flag)
		}
	}
}

func TestCaughtError(t *testing.T) {
	err := errors.New("caught")
	if !errors.Is(returns.CaughtError(err), err) {
		t.Fatal("error not preserved")
	}
}

func TestIgnoredResults(_ *testing.T) {
	returns.IgnoredFlag(true)
	returns.IgnoredFlag(false)
	_ = returns.IgnoredError(errors.New("ignored"))
}
