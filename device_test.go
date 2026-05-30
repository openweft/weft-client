package weftclient

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestDeviceAuth_MissingArgs(t *testing.T) {
	if _, err := DeviceAuth(context.Background(), "", "c", nil); err == nil {
		t.Fatal("expected error on empty issuer")
	}
	if _, err := DeviceAuth(context.Background(), "i", "", nil); err == nil {
		t.Fatal("expected error on empty client_id")
	}
}

func TestDeviceAuth_DefaultScopesAndInterval(t *testing.T) {
	var seenScope string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/device/code" {
			t.Errorf("bad path: %s", r.URL.Path)
		}
		_ = r.ParseForm()
		seenScope = r.Form.Get("scope")
		w.Header().Set("Content-Type", "application/json")
		// Interval=0 → exercises the default-5s clamp.
		fmt.Fprint(w, `{"device_code":"dc","user_code":"uc","verification_uri":"v","verification_uri_complete":"vc","expires_in":600,"interval":0}`)
	}))
	defer srv.Close()
	got, err := DeviceAuth(context.Background(), srv.URL+"/", "cid", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(seenScope, "openid") || !strings.Contains(seenScope, "groups") {
		t.Fatalf("default scopes missing: %q", seenScope)
	}
	if got.Interval != 5 {
		t.Fatalf("interval should default to 5, got %d", got.Interval)
	}
}

func TestDeviceAuth_CustomScopes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.Form.Get("scope") != "openid email" {
			t.Errorf("scope = %q", r.Form.Get("scope"))
		}
		fmt.Fprint(w, `{"device_code":"d","user_code":"u","verification_uri":"v","verification_uri_complete":"vc","expires_in":1,"interval":1}`)
	}))
	defer srv.Close()
	if _, err := DeviceAuth(context.Background(), srv.URL, "cid", []string{"openid", "email"}); err != nil {
		t.Fatal(err)
	}
}

func TestDeviceAuth_NonOKStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `{"error":"bad"}`)
	}))
	defer srv.Close()
	_, err := DeviceAuth(context.Background(), srv.URL, "cid", nil)
	if err == nil || !strings.Contains(err.Error(), "400") {
		t.Fatalf("unexpected: %v", err)
	}
}

func TestDeviceAuth_DecodeError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `not-json`)
	}))
	defer srv.Close()
	_, err := DeviceAuth(context.Background(), srv.URL, "cid", nil)
	if err == nil || !strings.Contains(err.Error(), "decode response") {
		t.Fatalf("unexpected: %v", err)
	}
}

func TestDeviceAuth_TransportError(t *testing.T) {
	// Closed server: connection refused.
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {}))
	srv.Close()
	_, err := DeviceAuth(context.Background(), srv.URL, "cid", nil)
	if err == nil {
		t.Fatal("expected transport error")
	}
}

func TestDeviceAuth_BadRequestBuild(t *testing.T) {
	// http.NewRequestWithContext rejects URLs containing control
	// characters. Inject a NUL byte to hit the build-request branch.
	_, err := DeviceAuth(context.Background(), "http://example\x00.com", "cid", nil)
	if err == nil || !strings.Contains(err.Error(), "build request") {
		t.Fatalf("unexpected: %v", err)
	}
}

func TestPollDeviceToken_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"access_token":"at","token_type":"Bearer","refresh_token":"rt","id_token":"idt","expires_in":3600}`)
	}))
	defer srv.Close()
	tok, idt, err := PollDeviceToken(context.Background(), srv.URL, "cid", &DeviceAuthResponse{
		DeviceCode: "dc",
		Interval:   1, // 1 second; first sleep before request.
		ExpiresIn:  60,
	})
	if err != nil {
		t.Fatal(err)
	}
	if tok.AccessToken != "at" || tok.RefreshToken != "rt" || idt != "idt" {
		t.Fatalf("got %+v idt=%s", tok, idt)
	}
}

func TestPollDeviceToken_DefaultDeadline_Success(t *testing.T) {
	// ExpiresIn == 0 forces the 10-minute default deadline path.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"access_token":"at","token_type":"Bearer","expires_in":60}`)
	}))
	defer srv.Close()
	_, _, err := PollDeviceToken(context.Background(), srv.URL, "cid", &DeviceAuthResponse{
		DeviceCode: "dc", Interval: 1, ExpiresIn: 0,
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestPollDeviceToken_AuthPendingThenSuccess(t *testing.T) {
	var n int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if atomic.AddInt32(&n, 1) < 2 {
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, `{"error":"authorization_pending"}`)
			return
		}
		fmt.Fprint(w, `{"access_token":"ok","expires_in":1}`)
	}))
	defer srv.Close()
	tok, _, err := PollDeviceToken(context.Background(), srv.URL, "cid", &DeviceAuthResponse{
		DeviceCode: "dc", Interval: 1, ExpiresIn: 60,
	})
	if err != nil {
		t.Fatal(err)
	}
	if tok.AccessToken != "ok" {
		t.Fatalf("got %+v", tok)
	}
}

func TestPollDeviceToken_SlowDownThenSuccess(t *testing.T) {
	var n int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if atomic.AddInt32(&n, 1) < 2 {
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, `{"error":"slow_down"}`)
			return
		}
		fmt.Fprint(w, `{"access_token":"ok","expires_in":1}`)
	}))
	defer srv.Close()
	// The slow_down branch widens interval by 5s. To keep the test
	// fast we tolerate a couple of seconds of wallclock here.
	done := make(chan struct{})
	go func() {
		_, _, err := PollDeviceToken(context.Background(), srv.URL, "cid", &DeviceAuthResponse{
			DeviceCode: "dc", Interval: 1, ExpiresIn: 60,
		})
		if err != nil {
			t.Errorf("err: %v", err)
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("timeout waiting for slow_down branch")
	}
}

func TestPollDeviceToken_ExpiredTokenErr(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `{"error":"expired_token"}`)
	}))
	defer srv.Close()
	_, _, err := PollDeviceToken(context.Background(), srv.URL, "cid", &DeviceAuthResponse{
		DeviceCode: "dc", Interval: 1, ExpiresIn: 60,
	})
	if err == nil || !strings.Contains(err.Error(), "code expired before authorisation") {
		t.Fatalf("unexpected err: %v", err)
	}
}

func TestPollDeviceToken_AccessDenied(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `{"error":"access_denied"}`)
	}))
	defer srv.Close()
	_, _, err := PollDeviceToken(context.Background(), srv.URL, "cid", &DeviceAuthResponse{
		DeviceCode: "dc", Interval: 1, ExpiresIn: 60,
	})
	if err == nil || !strings.Contains(err.Error(), "user denied") {
		t.Fatalf("unexpected: %v", err)
	}
}

func TestPollDeviceToken_UnknownError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `{"error":"some_other_thing"}`)
	}))
	defer srv.Close()
	_, _, err := PollDeviceToken(context.Background(), srv.URL, "cid", &DeviceAuthResponse{
		DeviceCode: "dc", Interval: 1, ExpiresIn: 60,
	})
	if err == nil || !strings.Contains(err.Error(), "poll token:") {
		t.Fatalf("unexpected: %v", err)
	}
}

func TestPollDeviceToken_DecodeSuccessError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `garbage`)
	}))
	defer srv.Close()
	_, _, err := PollDeviceToken(context.Background(), srv.URL, "cid", &DeviceAuthResponse{
		DeviceCode: "dc", Interval: 1, ExpiresIn: 60,
	})
	if err == nil || !strings.Contains(err.Error(), "decode success") {
		t.Fatalf("unexpected: %v", err)
	}
}

func TestPollDeviceToken_TransportError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {}))
	srv.Close()
	_, _, err := PollDeviceToken(context.Background(), srv.URL, "cid", &DeviceAuthResponse{
		DeviceCode: "dc", Interval: 1, ExpiresIn: 60,
	})
	if err == nil {
		t.Fatal("expected transport error")
	}
}

func TestPollDeviceToken_BuildRequestError(t *testing.T) {
	// NUL byte in URL triggers the inner build-request branch on
	// the second iteration — but the first call must succeed in
	// timing, so we use a URL the first request fails on.
	_, _, err := PollDeviceToken(context.Background(), "http://bad\x00", "cid", &DeviceAuthResponse{
		DeviceCode: "dc", Interval: 1, ExpiresIn: 60,
	})
	if err == nil || !strings.Contains(err.Error(), "build request") {
		t.Fatalf("unexpected: %v", err)
	}
}

func TestPollDeviceToken_CtxCancelled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `{"error":"authorization_pending"}`)
	}))
	defer srv.Close()
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()
	_, _, err := PollDeviceToken(ctx, srv.URL, "cid", &DeviceAuthResponse{
		DeviceCode: "dc", Interval: 1, ExpiresIn: 60,
	})
	if err == nil {
		t.Fatal("expected ctx error")
	}
}

func TestPollDeviceToken_DeadlineHit(t *testing.T) {
	// Use ExpiresIn=0 not OK because that triggers the 10-min
	// default; but we can construct a response with ExpiresIn very
	// short and a server that always says authorization_pending so
	// the deadline triggers on a subsequent iteration.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `{"error":"authorization_pending"}`)
	}))
	defer srv.Close()
	// ExpiresIn=1s, Interval=1s: first poll completes ~1s in (deadline ~1s),
	// loop top sees deadline passed → returns "code expired".
	_, _, err := PollDeviceToken(context.Background(), srv.URL, "cid", &DeviceAuthResponse{
		DeviceCode: "dc", Interval: 1, ExpiresIn: 1,
	})
	if err == nil || !strings.Contains(err.Error(), "code expired") {
		t.Fatalf("unexpected: %v", err)
	}
}
