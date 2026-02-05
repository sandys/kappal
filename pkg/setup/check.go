package setup

import (
	"fmt"
	"os"
)

// ErrNotSetUp is returned when kappal hasn't been set up
var ErrNotSetUp = fmt.Errorf("kappal is not set up. Run 'kappal --setup' first")

// Check verifies that setup has been completed in current directory
func Check() error {
	if _, err := os.Stat(MetadataPath()); os.IsNotExist(err) {
		return ErrNotSetUp
	}
	return nil
}

// IsSetUp returns true if setup has been completed
func IsSetUp() bool {
	return Check() == nil
}
