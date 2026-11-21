// SPDX-License-Identifier: GPL-3.0-or-later

package peer

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Fenron-dev/reposhelf/internal/testutil"
)

// TestPairingAndReplay: codes are long (60 bits), guessing invalidates
// them, and a signed request cannot be sent twice (R-021).
func TestPairingAndReplay(t *testing.T) {
	ctx := context.Background()
	f := testutil.NewForge(t)
	a := newInstance(t, f, "A")
	b := newInstance(t, f, "B")

	code, _ := a.m.NewCode()
	if len(normCode(code)) != codeChars || strings.Count(code, "-") != 2 {
		t.Errorf("code = %q", code)
	}
	for i := 0; i < maxMisses; i++ {
		b.m.Pair(ctx, a.http.URL, "AAAA-AAAA-AAAA")
	}
	if _, err := b.m.Pair(ctx, a.http.URL, code); err == nil {
		t.Fatal("code still valid after guessing")
	}
	code, _ = a.m.NewCode()
	if _, err := b.m.Pair(ctx, a.http.URL, code); err != nil {
		t.Fatal(err)
	}

	// A signed request is accepted once, not a second time.
	body := []byte(`{}`)
	req := httptest.NewRequest(http.MethodPost, "/peer/x", bytes.NewReader(body))
	b.m.sign(req, body)
	if _, err := a.m.verify(req, body); err != nil {
		t.Fatalf("first = %v", err)
	}
	if _, err := a.m.verify(req, body); !errors.Is(err, ErrBadSignature) {
		t.Errorf("replay = %v", err)
	}
	// Without nonce (old version): refused with a hint.
	req.Header.Del(hdrNonce)
	if _, err := a.m.verify(req, body); err == nil || !strings.Contains(err.Error(), "zu alt") {
		t.Errorf("no nonce = %v", err)
	}
}
