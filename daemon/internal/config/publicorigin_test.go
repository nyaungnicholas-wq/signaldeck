// Allowed HOSTS and web ORIGINS serve different purposes: HOSTS limit
// the HTTP Host header the daemon will accept, while ORIGINS limit the
// values of the Origin header sent by browsers for cross‑site requests.
// The default ORIGINS list contains four localhost spellings used during
// development: http://localhost:8323, http://127.0.0.1:8323,
// http://localhost:3000 and http://127.0.0.1:3000.
// When a deployment is published (PublicSurface == true) browsers send
// an https:// Origin, which is never present in that default list,
// causing PublicOriginMissing() to return true and the daemon to refuse
// to start. From the outside the failure is invisible because every GET
// still works, while every POST and the operator login receive 403.

package config

import "testing"

func TestPublicOriginMissingFiresOnlyOnAPublishedDeploymentWithNoHTTPSOrigin(t *testing.T) {
	tests := []struct {
		name string
		c    Config
		want bool
	}{
		{
			name: "dev box with defaults does not fire",
			c: Config{
				PublicSurface: false,
				WebOrigins: []string{
					"http://localhost:8323",
					"http://127.0.0.1:8323",
					"http://localhost:3000",
					"http://127.0.0.1:3000",
				},
			},
			want: false,
		},
		{
			name: "dev box with empty origins does not fire",
			c: Config{
				PublicSurface: false,
				WebOrigins:    []string{},
			},
			want: false,
		},
		{
			name: "published deployment with only localhost defaults fires",
			c: Config{
				PublicSurface: true,
				WebOrigins: []string{
					"http://localhost:8323",
					"http://127.0.0.1:8323",
					"http://localhost:3000",
					"http://127.0.0.1:3000",
				},
			},
			want: true,
		},
		{
			name: "published deployment with nil origins fires",
			c: Config{
				PublicSurface: true,
				WebOrigins:    nil,
			},
			want: true,
		},
		{
			name: "published deployment with a single HTTPS origin does not fire",
			c: Config{
				PublicSurface: true,
				WebOrigins:    []string{"https://signaldeck.example"},
			},
			want: false,
		},
		{
			name: "published deployment with localhost defaults plus an HTTPS origin does not fire",
			c: Config{
				PublicSurface: true,
				WebOrigins: []string{
					"http://localhost:8323",
					"http://127.0.0.1:8323",
					"http://localhost:3000",
					"http://127.0.0.1:3000",
					"https://signaldeck.example",
				},
			},
			want: false,
		},
		{
			name: "published deployment with uppercase HTTPS origin does not fire",
			c: Config{
				PublicSurface: true,
				WebOrigins:    []string{"HTTPS://SIGNALDECK.EXAMPLE"},
			},
			want: false,
		},
		{
			name: "published deployment with spaced HTTPS origin does not fire",
			c: Config{
				PublicSurface: true,
				WebOrigins:    []string{"  https://signaldeck.example  "},
			},
			want: false,
		},
		{
			name: "published deployment with only HTTP origin fires (write paths would 403)",
			c: Config{
				PublicSurface: true,
				WebOrigins:    []string{"http://signaldeck.example"},
			},
			want: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.c.PublicOriginMissing(); got != tt.want {
				if tt.want {
					t.Errorf("daemon would refuse to start on a configuration that is fine")
				} else {
					t.Errorf("daemon would start and serve a site whose write paths answer 403")
				}
			}
		})
	}
}
