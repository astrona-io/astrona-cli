package config

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestHTTPSOnlyClientRedirects(t *testing.T) {
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("plain"))
	}))
	defer plain.Close()

	var tlsSrv *httptest.Server
	tlsSrv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/to-http":
			http.Redirect(w, r, plain.URL+"/x", http.StatusFound)
		case "/to-https":
			http.Redirect(w, r, tlsSrv.URL+"/final", http.StatusFound)
		case "/loop":
			http.Redirect(w, r, tlsSrv.URL+"/loop", http.StatusFound)
		default:
			_, _ = w.Write([]byte("secure"))
		}
	}))
	defer tlsSrv.Close()

	tests := []struct {
		name    string
		path    string
		wantErr string
	}{
		{"https to http refused", "/to-http", "non-https"},
		{"https to https followed", "/to-https", ""},
		{"redirect cap kept", "/loop", "stopped after 10 redirects"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := HTTPSOnlyClient(5 * time.Second)
			client.Transport = tlsSrv.Client().Transport

			resp, err := client.Get(tlsSrv.URL + tt.path)
			if tt.wantErr != "" {
				if err == nil {
					resp.Body.Close()
					t.Fatalf("expected error containing %q, got none", tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("expected error containing %q, got %v", tt.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			defer resp.Body.Close()
			if resp.Request.URL.Path != "/final" {
				t.Fatalf("expected to land on /final, got %s", resp.Request.URL.Path)
			}
		})
	}
}
