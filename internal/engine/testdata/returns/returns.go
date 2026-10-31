// Package returns provides fixed return-site mutation acceptance fixtures.
package returns

// AssertedFlag returns a flag that tests assert.
func AssertedFlag(flag bool) bool { return flag }

// IgnoredFlag returns a flag that tests intentionally ignore.
func IgnoredFlag(flag bool) bool { return flag }

// CaughtError returns an error that tests assert.
func CaughtError(err error) error { return err }

// IgnoredError returns an error that tests intentionally ignore.
func IgnoredError(err error) error { return err }
