package config

import (
	"fmt"
	"math"
	"strings"
)

// Validate rejects values that would overflow rotation sizes or disable quota scoring.
// Zero retains the documented defaults for existing configurations.
func (c QuotaShadowConfig) Validate() error {
	if c.MaxFileMB < 0 || c.MaxFileMB > 65536 {
		return fmt.Errorf("routing.quota-shadow.max-file-mb must be between 0 and 65536")
	}
	if math.IsNaN(c.SoftLimitPercent) || math.IsInf(c.SoftLimitPercent, 0) || c.SoftLimitPercent < 0 || c.SoftLimitPercent > 100 {
		return fmt.Errorf("routing.quota-shadow.soft-limit-percent must be between 0 and 100")
	}
	if len(c.File) > 4096 || strings.ContainsAny(c.File, "\x00\r\n") {
		return fmt.Errorf("routing.quota-shadow.file must be a valid path of at most 4096 bytes")
	}
	return nil
}
