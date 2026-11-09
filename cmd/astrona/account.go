package main

import (
	"context"

	"astrona/internal/labops"
)

// accountForLab returns the active account for optional catalog session tracking.
// Local labs and signed-out catalog labs run without a session. Credential
// storage and renewal failures are still reported rather than hidden.
func accountForLab(ctx context.Context, flags *rootFlags) (*labops.Account, error) {
	return labops.AccountForLab(ctx, flags.catalogLab, flags.localOnly || flags.headless)
}
