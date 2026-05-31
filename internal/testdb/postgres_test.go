package testdb

import (
	"errors"
	"testing"
)

func TestIsContainerRuntimeUnavailable(t *testing.T) {
	for _, message := range []string{
		"get provider: checked path: $XDG_RUNTIME_DIR",
		"checked path: $XDG_RUNTIME_DIR, failed to create Docker provider",
	} {
		err := WrapContainerRuntimeError(errors.New(message))
		if !IsContainerRuntimeUnavailable(err) {
			t.Fatalf("IsContainerRuntimeUnavailable(%v) = false, want true", err)
		}
	}
}

func TestWrapContainerRuntimeErrorLeavesDatabaseErrorsAlone(t *testing.T) {
	err := errors.New("postgres startup timeout")
	if wrapped := WrapContainerRuntimeError(err); wrapped != err {
		t.Fatalf("WrapContainerRuntimeError() = %v, want original error", wrapped)
	}
}
