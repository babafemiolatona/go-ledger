package handlers

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-ledger/internal/ledger"
	"github.com/go-ledger/internal/testhelpers"
	"github.com/google/uuid"
)

func TestPrivilege_POSTUsersWithServiceRoleIsIgnored(t *testing.T) {
	pool := testhelpers.TestPool(t)
	testhelpers.AcquireAdvisoryLock(t, pool)
	testhelpers.TruncateAll(t, pool)
	svc := ledger.New(pool)
	h := CreateUser(svc)

	email := "priv-test-" + uuid.NewString() + "@example.com"
	body, _ := json.Marshal(map[string]string{"email": email, "role": "service"})
	req := httptest.NewRequest(http.MethodPost, "/v1/users", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status %d want 201 body %s", rec.Code, rec.Body.String())
	}
	var role string
	err := pool.QueryRow(context.Background(), `SELECT role FROM api_keys WHERE user_id = (SELECT id FROM users WHERE email=$1)`, email).Scan(&role)
	if err != nil {
		t.Fatalf("query role: %v", err)
	}
	if role != "user" {
		t.Fatalf("DB role %q want 'user' — privilege escalation via POST /v1/users", role)
	}
}

func TestPrivilege_UserCannotCreateServiceKey(t *testing.T) {
	pool := testhelpers.TestPool(t)
	testhelpers.AcquireAdvisoryLock(t, pool)
	testhelpers.TruncateAll(t, pool)
	svc := ledger.New(pool)

	email := "user-caller-" + uuid.NewString() + "@example.com"
	hCreateUser := CreateUser(svc)
	body, _ := json.Marshal(map[string]string{"email": email})
	req := httptest.NewRequest(http.MethodPost, "/v1/users", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	hCreateUser.ServeHTTP(rec, req)
	if rec.Code != 201 {
		t.Fatalf("create user: %d %s", rec.Code, rec.Body.String())
	}
	var created map[string]string
	_ = json.Unmarshal(rec.Body.Bytes(), &created)
	rawKey := created["api_key"]
	uid, role, _, _, err := svc.Authenticate(context.Background(), rawKey)
	if err != nil || role != "user" {
		t.Fatalf("authenticate user: %v role %q", err, role)
	}

	hCreateKey := CreateApiKey(svc)
	body2, _ := json.Marshal(map[string]string{"role": "service"})
	req2 := httptest.NewRequest(http.MethodPost, "/v1/api-keys", bytes.NewReader(body2))
	req2.Header.Set("Content-Type", "application/json")
	req2 = req2.WithContext(ledger.WithCaller(req2.Context(), uid, role, mustParseUUID(t, created["key_id"]), rawKey[:12]))
	rec2 := httptest.NewRecorder()
	hCreateKey.ServeHTTP(rec2, req2)
	if rec2.Code != 403 {
		t.Fatalf("user creating service key should 403, got %d body %s", rec2.Code, rec2.Body.String())
	}
	var cnt int
	err = pool.QueryRow(context.Background(), `SELECT count(*) FROM api_keys WHERE user_id=$1 AND role='service'`, uid).Scan(&cnt)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if cnt != 0 {
		t.Fatalf("DB has %d service rows for user caller, should be 0", cnt)
	}
}

func TestPrivilege_ServiceCanCreateServiceKey(t *testing.T) {
	pool := testhelpers.TestPool(t)
	testhelpers.AcquireAdvisoryLock(t, pool)
	testhelpers.TruncateAll(t, pool)
	svc := ledger.New(pool)

	email := "service-caller-" + uuid.NewString() + "@example.com"
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		t.Fatalf("rand: %v", err)
	}
	rawService := "sk_live_" + hex.EncodeToString(b)
	h := sha256.Sum256([]byte(strings.TrimSpace(rawService)))
	hash := hex.EncodeToString(h[:])
	prefix := rawService[:12]
	var svcUserID uuid.UUID
	err := pool.QueryRow(context.Background(), `INSERT INTO users (email) VALUES ($1) RETURNING id`, email).Scan(&svcUserID)
	if err != nil {
		t.Fatalf("insert service user: %v", err)
	}
	var svcKeyID uuid.UUID
	err = pool.QueryRow(context.Background(), `INSERT INTO api_keys (user_id, key_hash, key_prefix, role) VALUES ($1,$2,$3,'service') RETURNING id`, svcUserID, hash, prefix).Scan(&svcKeyID)
	if err != nil {
		t.Fatalf("insert service key: %v", err)
	}
	uid, role, _, _, err := svc.Authenticate(context.Background(), rawService)
	if err != nil || role != "service" {
		t.Fatalf("authenticate service: %v role %q", err, role)
	}
	hCreateKey := CreateApiKey(svc)
	body, _ := json.Marshal(map[string]string{"role": "service"})
	req := httptest.NewRequest(http.MethodPost, "/v1/api-keys", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(ledger.WithCaller(req.Context(), uid, role, svcKeyID, prefix))
	rec := httptest.NewRecorder()
	hCreateKey.ServeHTTP(rec, req)
	if rec.Code != 201 {
		t.Fatalf("service creating service key should 201, got %d body %s", rec.Code, rec.Body.String())
	}
	var cnt int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM api_keys WHERE user_id=$1 AND role='service'`, svcUserID).Scan(&cnt); err != nil {
		t.Fatalf("count: %v", err)
	}
	if cnt < 2 {
		t.Fatalf("expected >=2 service rows, got %d", cnt)
	}
}

func mustParseUUID(t *testing.T, s string) uuid.UUID {
	t.Helper()
	id, err := uuid.Parse(s)
	if err != nil {
		t.Fatalf("parse uuid: %v", err)
	}
	return id
}
