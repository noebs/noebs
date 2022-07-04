package ebs_fields

import (
	"time"
)

// SystemClock is a clock that returns local time of the system.
var SystemClock = &systemClock{}

// Clock is used to query the current local time.
type Clock interface {
	Now() time.Time
}

// systemClock returns the current system time.
type systemClock struct{}

// Now returns the current system time by calling time.Now().
func (s *systemClock) Now() time.Time {
	return time.Now()
}

// MockClock can be used to mock current time during tests.
type MockClock struct {
	Timestamp time.Time
}

// Now returns the timestamp set in the MockClock.
func (m *MockClock) Now() time.Time {
	return m.Timestamp
}
