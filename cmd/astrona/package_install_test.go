package main

import (
	"strings"
	"testing"
)

func TestPackageToolPath(t *testing.T) {
	for _, format := range []string{"deb", "rpm", "snap"} {
		got := packageToolPath("/usr/lib/astrona/astrona", "/usr/bin:/bin", format)
		if got != "/usr/lib/astrona/tools:/usr/bin:/bin" {
			t.Fatalf("%s: %s", format, got)
		}
	}
	if got := packageToolPath("/tmp/astrona", "/usr/bin", ""); got != "/usr/bin" {
		t.Fatalf("bare binary PATH changed: %s", got)
	}
}

func TestPackagedUpgradeDoesNotReplaceBinary(t *testing.T) {
	original := PackageFormat
	t.Cleanup(func() { PackageFormat = original })
	for _, format := range []string{"deb", "rpm", "snap"} {
		PackageFormat = format
		for _, force := range []bool{false, true} {
			cmd := newUpgradeCmd()
			if force {
				if err := cmd.Flags().Set("force", "true"); err != nil {
					t.Fatal(err)
				}
			}
			err := cmd.RunE(cmd, nil)
			if err == nil || !strings.Contains(err.Error(), packageUpgradeInstruction(format)) {
				t.Fatalf("%s force=%v: expected package instructions, got %v", format, force, err)
			}
		}
	}
}
