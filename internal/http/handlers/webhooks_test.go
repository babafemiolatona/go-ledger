package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-ledger/internal/ledger"
	"github.com/go-ledger/internal/testhelpers"
	"github.com/google/uuid"
)

func TestWebhooks_CreateAndListScopedByOwner(t *testing.T) {
	pool := testhelpers.TestPool(t)
	testhelpers.AcquireAdvisoryLock(t, pool)
	testhelpers.TruncateAll(t, pool)
	svc := ledger.New(pool)

	ownerA := testhelpers.CreateUser(t, pool, "wha-"+uuid.NewString()+"@test.local")
	ownerB := testhelpers.CreateUser(t, pool, "whb-"+uuid.NewString()+"@test.local")

	body, _ := json.Marshal(map[string]string{"url": "http://127.0.0.1:9/hook"})
	req := httptest.NewRequest(http.MethodPost, "/v1/webhooks", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(ledger.WithCaller(req.Context(), ownerA, "user", uuid.New(), "sk_live_test"))
	rec := httptest.NewRecorder()
	CreateWebhook(svc)(rec, req)
	if rec.Code != 201 {
		t.Fatalf("create: %d %s", rec.Code, rec.Body.String())
	}
	var created map[string]string
	_ = json.Unmarshal(rec.Body.Bytes(), &created)
	if created["secret"] == "" {
		t.Fatalf("secret not returned once on create: %v", created)
	}
	whID := created["id"]

	reqB := httptest.NewRequest(http.MethodGet, "/v1/webhooks", nil)
	reqB = reqB.WithContext(ledger.WithCaller(reqB.Context(), ownerB, "user", uuid.New(), "sk_live_test"))
	recB := httptest.NewRecorder()
	ListWebhooks(svc)(recB, reqB)
	if recB.Code != 200 {
		t.Fatalf("list: %d", recB.Code)
	}
	var listed map[string][]map[string]any
	_ = json.Unmarshal(recB.Body.Bytes(), &listed)
	if len(listed["webhooks"]) != 0 {
		t.Fatalf("user B sees %d webhooks, want 0", len(listed["webhooks"]))
	}
	for _, ep := range listed["webhooks"] {
		if _, ok := ep["secret"]; ok {
			t.Fatalf("secret leaked in list")
		}
	}

	reqD := httptest.NewRequest(http.MethodGet, "/v1/webhooks/deliveries?webhook_id="+whID, nil)
	reqD = reqD.WithContext(ledger.WithCaller(reqD.Context(), ownerB, "user", uuid.New(), "sk_live_test"))
	recD := httptest.NewRecorder()
	ListWebhookDeliveries(svc)(recD, reqD)
	if recD.Code != 404 {
		t.Fatalf("cross-owner deliveries: %d want 404", recD.Code)
	}

	reqS := httptest.NewRequest(http.MethodGet, "/v1/webhooks", nil)
	reqS = reqS.WithContext(ledger.WithCaller(reqS.Context(), uuid.New(), "service", uuid.New(), "sk_live_test"))
	recS := httptest.NewRecorder()
	ListWebhooks(svc)(recS, reqS)
	var all map[string][]map[string]any
	_ = json.Unmarshal(recS.Body.Bytes(), &all)
	if len(all["webhooks"]) != 1 {
		t.Fatalf("service sees %d webhooks, want 1", len(all["webhooks"]))
	}

	bad, _ := json.Marshal(map[string]string{"url": "ftp://x/y"})
	reqBad := httptest.NewRequest(http.MethodPost, "/v1/webhooks", bytes.NewReader(bad))
	reqBad.Header.Set("Content-Type", "application/json")
	reqBad = reqBad.WithContext(ledger.WithCaller(reqBad.Context(), ownerA, "user", uuid.New(), "sk_live_test"))
	recBad := httptest.NewRecorder()
	CreateWebhook(svc)(recBad, reqBad)
	if recBad.Code != 400 {
		t.Fatalf("bad url: %d want 400", recBad.Code)
	}
}
