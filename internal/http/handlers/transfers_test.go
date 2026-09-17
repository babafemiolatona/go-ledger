package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-ledger/internal/ledger"
	"github.com/go-ledger/internal/testhelpers"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

func callerCtxForAccount(t *testing.T, svc *ledger.Service, acctID uuid.UUID) context.Context {
	t.Helper()
	owner, err := svc.GetAccountOwner(t.Context(), acctID)
	if err != nil {
		t.Fatalf("get owner: %v", err)
	}
	return ledger.WithCaller(t.Context(), owner, "user", uuid.New(), "test")
}

func TestTransferHandler_InsufficientFunds409(t *testing.T) {
	pool := testhelpers.TestPool(t)
	testhelpers.AcquireAdvisoryLock(t, pool)
	src, dst := testhelpers.NewFundedPair(t, pool, 5000)
	svc := ledger.New(pool)
	h := Transfer(svc)

	body, _ := json.Marshal(map[string]string{
		"from_account_id": src.String(),
		"to_account_id":   dst.String(),
		"amount":          "100.00",
		"currency":        "USD",
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/transfers", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(callerCtxForAccount(t, svc, src))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status %d want 409, body %s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
}

func TestTransferHandler_PgError23514MapsTo409(t *testing.T) {
	pool := testhelpers.TestPool(t)
	testhelpers.AcquireAdvisoryLock(t, pool)
	testhelpers.TruncateAll(t, pool)

	var err error = &pgconn.PgError{Code: "23514", Message: "insufficient_funds: account would go negative"}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23514" {
		t.Fatalf("pg error not detected")
	}

	src, dst := testhelpers.NewFundedPair(t, pool, 1000)
	svc := ledger.New(pool)
	h := Transfer(svc)

	_, _ = svc.Transfer(t.Context(), src, dst, 1000, "USD")

	body, _ := json.Marshal(map[string]string{
		"from_account_id": src.String(),
		"to_account_id":   dst.String(),
		"amount":          "1.00",
		"currency":        "USD",
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/transfers", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(callerCtxForAccount(t, svc, src))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("after drain: status %d want 409, body %s", rec.Code, rec.Body.String())
	}

	body2, _ := json.Marshal(map[string]string{
		"from_account_id": uuid.New().String(),
		"to_account_id":   dst.String(),
		"amount":          "1.00",
		"currency":        "USD",
	})
	req2 := httptest.NewRequest(http.MethodPost, "/v1/transfers", bytes.NewReader(body2))
	req2.Header.Set("Content-Type", "application/json")
	req2 = req2.WithContext(callerCtxForAccount(t, svc, src))
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusNotFound {
		t.Fatalf("fake src: status %d want 404 body %s", rec2.Code, rec2.Body.String())
	}
}

func TestTransferHandler_Validation(t *testing.T) {
	pool := testhelpers.TestPool(t)
	testhelpers.AcquireAdvisoryLock(t, pool)
	src, dst := testhelpers.NewFundedPair(t, pool, 10000)
	svc := ledger.New(pool)
	h := Transfer(svc)

	body, _ := json.Marshal(map[string]string{
		"from_account_id": src.String(),
		"to_account_id":   src.String(),
		"amount":          "1.00",
		"currency":        "USD",
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/transfers", bytes.NewReader(body))
	req = req.WithContext(callerCtxForAccount(t, svc, src))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("same account: status %d want 422 body %s", rec.Code, rec.Body.String())
	}

	body, _ = json.Marshal(map[string]string{
		"from_account_id": src.String(),
		"to_account_id":   dst.String(),
		"amount":          "0.00",
		"currency":        "USD",
	})
	req = httptest.NewRequest(http.MethodPost, "/v1/transfers", bytes.NewReader(body))
	req = req.WithContext(callerCtxForAccount(t, svc, src))
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("zero amount: status %d want 422", rec.Code)
	}
}
