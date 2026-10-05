package config

import "fmt"

// EffectivePoints is a check/script's weight: Points, or 1 when unset.
func EffectivePoints(points int) int {
	if points <= 0 {
		return 1
	}
	return points
}

// ValidateScoring checks hint/points/passPercent values across the lab's
// validation blocks (the root one and each qemu VM's).
func ValidateScoring(cfg *LabConfig) error {
	blocks := map[string]ValidationConfig{"validation": cfg.Validation}
	for _, vm := range cfg.Runtime.QEMU {
		if vm.Validation != nil {
			blocks["runtime.qemu["+vm.Name+"].validation"] = *vm.Validation
		}
	}
	for where, v := range blocks {
		if v.PassPercent < 0 || v.PassPercent > 100 {
			return fmt.Errorf("%s.passPercent %d must be between 0 and 100", where, v.PassPercent)
		}
		for i, c := range v.Checks {
			if c.Points < 0 {
				return fmt.Errorf("%s.checks[%d].points %d can't be negative", where, i, c.Points)
			}
		}
		scripts := v.Scripts
		if v.Script != nil {
			scripts = append([]ResourceItem{*v.Script}, scripts...)
		}
		for _, s := range scripts {
			if s.Points < 0 {
				return fmt.Errorf("%s script '%s': points %d can't be negative", where, s.Name, s.Points)
			}
		}
	}
	return nil
}
