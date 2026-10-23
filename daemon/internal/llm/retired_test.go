package llm

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

const nvidiaBase = "https://integrate.api.nvidia.com/v1"

// countingRT counts round trips per host class; nvidia hosts never reach next.
type countingRT struct {
	next          http.RoundTripper
	nvidia, other atomic.Int32
}

func (c *countingRT) RoundTrip(r *http.Request) (*http.Response, error) {
	if h := strings.ToLower(r.URL.Hostname()); h == "nvidia.com" || strings.HasSuffix(h, ".nvidia.com") {
		c.nvidia.Add(1)
		return nil, errors.New("test transport: a request to an NVIDIA host was attempted")
	}
	c.other.Add(1)
	if c.next == nil {
		return nil, errors.New("test transport: no request was expected at all")
	}
	return c.next.RoundTrip(r)
}

var msgs = []Message{{Role: "user", Content: "hi"}}

// (a) New with an NVIDIA base URL and a key yields a disabled client that names
// its reason, and neither Complete nor CompleteWith reaches the transport.
func TestNVIDIAProviderRetired(t *testing.T) {
	for _, u := range []string{nvidiaBase, "https://INTEGRATE.API.NVIDIA.COM/v1/", "integrate.api.nvidia.com/v1",
		"https://nvidia.com", "https://foo.nvidia.com.:443/v1", "https://k@ai.api.nvidia.com/v1"} {
		if bad, why := ProviderRetired(u); !bad || !strings.Contains(why, "NVIDIA trial API terms do not permit production use") {
			t.Errorf("ProviderRetired(%q) = %v, %q; want retired with the NVIDIA reason", u, bad, why)
		}
	}
	for _, u := range []string{"https://api.example.com/v1", "https://notnvidia.com/v1", "https://nvidia.com.evil.example/v1", "http://127.0.0.1:1234/v1"} {
		if bad, _ := ProviderRetired(u); bad {
			t.Errorf("ProviderRetired(%q) = true; only nvidia.com and its subdomains are retired", u)
		}
	}

	c := New([]string{"nvapi-test-key"}, nvidiaBase, "m", "", "", 10)
	rt := &countingRT{}
	c.(*httpClient).http.Transport = rt
	if c.Enabled() || c.(Tiered).KeyCount() != 0 {
		t.Fatalf("a client for a retired provider must hold no keys: enabled=%v", c.Enabled())
	}
	if got := RetiredReason(c); !strings.Contains(got, "NVIDIA trial API terms") {
		t.Fatalf("RetiredReason = %q", got)
	}
	if _, err := c.Complete(context.Background(), "s", msgs, 16); err == nil {
		t.Fatal("Complete on a retired provider must fail")
	}
	if _, err := c.(Tiered).CompleteWith(context.Background(), "m", "s", msgs, 16); err == nil {
		t.Fatal("CompleteWith on a retired provider must fail")
	}
	if n := rt.nvidia.Load() + rt.other.Load(); n != 0 {
		t.Fatalf("%d round trip(s) attempted for a retired provider", n)
	}
}

// (b) A client built some other way, keys present, still never dials a retired
// host: the per-attempt guard refuses before the transport.
func TestAttemptRefusesRetiredHostWithoutDialing(t *testing.T) {
	rt := &countingRT{}
	c := &httpClient{keys: []string{"k"}, baseURL: nvidiaBase, model: "m", deepModel: "m", fastModel: "m",
		dailyCap: 10, http: &http.Client{Transport: rt}}
	if !c.Enabled() {
		t.Fatal("precondition: this client holds a key")
	}
	_, err := c.Complete(context.Background(), "s", msgs, 16)
	if !errors.Is(err, ErrRetired) {
		t.Fatalf("err = %v, want ErrRetired", err)
	}
	if n := rt.nvidia.Load() + rt.other.Load(); n != 0 {
		t.Fatalf("%d round trip(s) reached the transport", n)
	}
}

// (b2) A permitted host that redirects to NVIDIA must not carry the request on.
func TestRedirectToRetiredHostIsRefused(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, nvidiaBase+"/chat/completions", http.StatusTemporaryRedirect)
	}))
	defer srv.Close()
	c := New([]string{"k"}, srv.URL+"/v1", "m", "", "", 10)
	rt := &countingRT{next: http.DefaultTransport}
	c.(*httpClient).http.Transport = rt
	if _, err := c.Complete(context.Background(), "s", msgs, 16); err == nil {
		t.Fatal("a redirect to a retired host must fail the call")
	}
	if n := rt.nvidia.Load(); n != 0 {
		t.Fatalf("%d request(s) followed the redirect to NVIDIA", n)
	}
	if rt.other.Load() == 0 {
		t.Fatal("the permitted host was never called; the test proves nothing")
	}
}

// (c) Positive control: a non-retired base URL still works through the same
// counting transport.
func TestNonRetiredProviderStillWorks(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(jsonResp("ok")))
	}))
	defer srv.Close()
	c := New([]string{"k"}, srv.URL+"/v1", "m", "", "", 10)
	rt := &countingRT{next: http.DefaultTransport}
	c.(*httpClient).http.Transport = rt
	if !c.Enabled() || RetiredReason(c) != "" {
		t.Fatalf("a permitted provider must be enabled and not retired: %q", RetiredReason(c))
	}
	out, err := c.Complete(context.Background(), "s", msgs, 16)
	if err != nil || out != "ok" {
		t.Fatalf("Complete = %q, %v", out, err)
	}
	if rt.other.Load() != 1 || rt.nvidia.Load() != 0 {
		t.Fatalf("round trips: other=%d nvidia=%d, want 1/0", rt.other.Load(), rt.nvidia.Load())
	}
}
