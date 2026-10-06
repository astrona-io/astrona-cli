package scripts

import (
	"strings"
	"testing"

	"astrona/internal/config"
)

func TestResolveLocalSourceURLRequiresHTTPS(t *testing.T) {
	tests := []struct {
		name    string
		source  string
		wantErr bool
	}{
		{"https accepted", "https://example.com/m.yaml", false},
		{"http refused", "http://example.com/m.yaml", true},
		{"no scheme refused", "example.com/m.yaml", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			item := config.ResourceItem{Name: "m", Type: "url", Source: tt.source}
			got, err := ResolveLocalSource(item, t.TempDir())
			if tt.wantErr {
				if err == nil || !strings.Contains(err.Error(), "only https:// sources are allowed") {
					t.Fatalf("expected https refusal, got %v", err)
				}
				return
			}
			if err != nil || got != tt.source {
				t.Fatalf("got (%q, %v), want (%q, nil)", got, err, tt.source)
			}
		})
	}
}
