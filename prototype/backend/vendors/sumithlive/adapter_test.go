package sumithlive

import (
	"testing"
	"time"
)

// A grid of cameras opening at once must share one token, not authenticate
// per tile. Chemito proved the cost of the alternative: one session per
// account there, so racing logins dropped all but the last stream.
func TestAccessTokenIsCachedAndReused(t *testing.T) {
	a := New(Config{})
	a.token, a.tokenAt = "cached-token", time.Now()

	for i := 0; i < 5; i++ {
		tok, reused, err := a.accessToken()
		if err != nil || tok != "cached-token" || !reused {
			t.Fatalf("call %d = (%q, reused=%v, %v), want the cached token", i, tok, reused, err)
		}
	}

	// Past the TTL it must not keep serving a stale token. No server here,
	// so the re-auth fails — the point is that it tried rather than
	// returning the expired one.
	a.tokenAt = time.Now().Add(-tokenTTL - time.Second)
	if tok, _, err := a.accessToken(); err == nil {
		t.Fatalf("expired token still served (%q), want a re-auth attempt", tok)
	}
}

// forgetToken must force the next call to re-authenticate, so a rejected
// token isn't retried forever.
func TestForgetTokenClearsCache(t *testing.T) {
	a := New(Config{})
	a.token, a.tokenAt = "cached-token", time.Now()
	a.forgetToken()
	if tok, reused, err := a.accessToken(); err == nil || reused {
		t.Fatalf("got (%q, reused=%v, %v), want a re-auth attempt after forgetToken", tok, reused, err)
	}
}
