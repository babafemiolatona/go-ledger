package ledger

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-ledger/internal/testhelpers"
	"github.com/google/uuid"
)

type receiver struct {
	t         *testing.T
	secret    atomic.Value
	hits      atomic.Int32
	failN     int32
	lastTopic atomic.Value
	lastSigOK atomic.Bool
}

func (r *receiver) handler(w http.ResponseWriter, req *http.Request) {
	n := r.hits.Add(1)
	buf, _ := io.ReadAll(req.Body)
	secret, _ := r.secret.Load().(string)
	r.lastSigOK.Store(VerifyWebhookSignature(secret, buf, req.Header.Get("X-Webhook-Signature")))
	r.lastTopic.Store(req.Header.Get("X-Webhook-Topic"))
	if n <= r.failN {
		w.WriteHeader(500)
		return
	}
	w.WriteHeader(200)
}

func setupEndpoint(t *testing.T, failN int32) (*Service, *receiver, WebhookEndpoint) {
	t.Helper()
	pool := testhelpers.TestPool(t)
	testhelpers.AcquireAdvisoryLock(t, pool)
	testhelpers.TruncateAll(t, pool)
	svc := New(pool)

	r := &receiver{t: t, failN: failN}
	srv := httptest.NewServer(http.HandlerFunc(r.handler))
	t.Cleanup(srv.Close)

	owner := testhelpers.CreateUser(t, pool, "wh-"+uuid.NewString()+"@test.local")
	ep, secret, err := svc.CreateWebhookEndpoint(context.Background(), owner, srv.URL+"/hook")
	if err != nil {
		t.Fatalf("create endpoint: %v", err)
	}
	r.secret.Store(secret)
	return svc, r, ep
}

func forceDue(t *testing.T, svc *Service) {
	t.Helper()
	_, err := svc.Pool().Exec(context.Background(),
		`UPDATE webhook_deliveries SET next_retry_at=now()-interval '1 second' WHERE status='queued'`)
	if err != nil {
		t.Fatalf("force due: %v", err)
	}
}

func deliveryState(t *testing.T, svc *Service, ep WebhookEndpoint) (status string, attempts int) {
	t.Helper()
	err := svc.Pool().QueryRow(context.Background(),
		`SELECT status, attempts FROM webhook_deliveries WHERE endpoint_id=$1`, ep.ID).Scan(&status, &attempts)
	if err != nil {
		t.Fatalf("delivery state: %v", err)
	}
	return
}

func testClient() *http.Client { return &http.Client{Timeout: WebhookHTTPTimeout} }

func TestWebhooks_DeliverSuccess(t *testing.T) {
	svc, r, ep := setupEndpoint(t, 0)
	ctx := context.Background()
	if n, err := svc.FanoutWebhooks(ctx, "ledger.transfer.posted", []byte(`{"transaction_id":"abc"}`)); err != nil || n != 1 {
		t.Fatalf("fanout n=%d err=%v", n, err)
	}
	if n, err := svc.DeliverDueWebhooks(ctx, testClient()); err != nil || n != 1 {
		t.Fatalf("deliver n=%d err=%v", n, err)
	}
	if st, at := deliveryState(t, svc, ep); st != "delivered" || at != 1 {
		t.Fatalf("state %q attempts %d want delivered/1", st, at)
	}
	if r.hits.Load() != 1 || !r.lastSigOK.Load() {
		t.Fatalf("hits=%d sigOK=%v", r.hits.Load(), r.lastSigOK.Load())
	}
	if topic, _ := r.lastTopic.Load().(string); topic != "ledger.transfer.posted" {
		t.Fatalf("topic %q", topic)
	}
}

func TestWebhooks_RetryThenSuccess(t *testing.T) {
	svc, r, ep := setupEndpoint(t, 2)
	ctx := context.Background()
	if _, err := svc.FanoutWebhooks(ctx, "ledger.hold.created", []byte(`{}`)); err != nil {
		t.Fatalf("fanout: %v", err)
	}
	for i := 0; i < 3; i++ {
		forceDue(t, svc)
		if _, err := svc.DeliverDueWebhooks(ctx, testClient()); err != nil {
			t.Fatalf("deliver %d: %v", i, err)
		}
	}
	if st, at := deliveryState(t, svc, ep); st != "delivered" || at != 3 {
		t.Fatalf("state %q attempts %d want delivered/3", st, at)
	}
	if r.hits.Load() != 3 {
		t.Fatalf("hits=%d want 3", r.hits.Load())
	}
}

func TestWebhooks_DeadLetter(t *testing.T) {
	svc, r, ep := setupEndpoint(t, 1<<30) // always 500
	ctx := context.Background()
	if _, err := svc.FanoutWebhooks(ctx, "ledger.transfer.posted", []byte(`{}`)); err != nil {
		t.Fatalf("fanout: %v", err)
	}
	for i := 0; i < WebhookMaxAttempts; i++ {
		forceDue(t, svc)
		if _, err := svc.DeliverDueWebhooks(ctx, testClient()); err != nil {
			t.Fatalf("deliver %d: %v", i, err)
		}
	}
	if st, at := deliveryState(t, svc, ep); st != "dead" || at != WebhookMaxAttempts {
		t.Fatalf("state %q attempts %d want dead/%d", st, at, WebhookMaxAttempts)
	}

	forceDue(t, svc)
	if n, err := svc.DeliverDueWebhooks(ctx, testClient()); err != nil || n != 0 {
		t.Fatalf("after dead: n=%d err=%v", n, err)
	}
	if r.hits.Load() != int32(WebhookMaxAttempts) {
		t.Fatalf("hits=%d want %d", r.hits.Load(), WebhookMaxAttempts)
	}
}

func TestWebhooks_ConcurrentWorkersNeverDoubleDeliver(t *testing.T) {
	svc, _, ep := setupEndpoint(t, 0)
	ctx := context.Background()

	// slow receiver so the two workers overlap while delivering
	var hits atomic.Int32
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		time.Sleep(150 * time.Millisecond)
		w.WriteHeader(200)
	}))
	t.Cleanup(slow.Close)
	if _, err := svc.Pool().Exec(ctx, `UPDATE webhook_endpoints SET url=$2 WHERE id=$1`, ep.ID, slow.URL+"/hook"); err != nil {
		t.Fatalf("repoint endpoint: %v", err)
	}

	if n, err := svc.FanoutWebhooks(ctx, "ledger.transfer.posted", []byte(`{}`)); err != nil || n != 1 {
		t.Fatalf("fanout n=%d err=%v", n, err)
	}
	// 3 more deliveries for the same endpoint
	for i := 0; i < 3; i++ {
		if _, err := svc.Pool().Exec(ctx,
			`INSERT INTO webhook_deliveries (endpoint_id, topic, payload) VALUES ($1,'ledger.transfer.posted','{}')`, ep.ID); err != nil {
			t.Fatalf("seed delivery: %v", err)
		}
	}

	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errs[i] = svc.DeliverDueWebhooks(ctx, testClient())
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("worker %d: %v", i, err)
		}
	}
	// 4 deliveries, 4 HTTP hits total: the atomic claim gave each row to
	// exactly one worker. (With select-then-update, both workers would race
	// on the same unlocked rows and hits would exceed 4.)
	if got := hits.Load(); got != 4 {
		t.Fatalf("receiver hits=%d want 4 (double delivery!)", got)
	}
	var bad int
	if err := svc.Pool().QueryRow(ctx,
		`SELECT count(*) FROM webhook_deliveries WHERE status!='delivered' OR attempts!=1`).Scan(&bad); err != nil {
		t.Fatalf("count: %v", err)
	}
	if bad != 0 {
		t.Fatalf("%d deliveries not (delivered, attempts=1)", bad)
	}
}

func TestWebhooks_BackoffIncreases(t *testing.T) {
	prev := WebhookBackoff(1)
	if prev != 5_000_000_000 {
		t.Fatalf("attempt 1 backoff %v want 5s", prev)
	}
	for a := 2; a <= 6; a++ {
		d := WebhookBackoff(a)
		if d <= prev {
			t.Fatalf("attempt %d backoff %v not > %v", a, d, prev)
		}
		prev = d
	}
	if got := WebhookBackoff(100); got != WebhookHTTPTimeout*360 {
		t.Fatalf("cap %v want 1h", got)
	}
}

func TestWebhooks_FanoutOnlyActive(t *testing.T) {
	pool := testhelpers.TestPool(t)
	testhelpers.AcquireAdvisoryLock(t, pool)
	testhelpers.TruncateAll(t, pool)
	svc := New(pool)
	ctx := context.Background()

	owner := testhelpers.CreateUser(t, pool, "wh2-"+uuid.NewString()+"@test.local")
	if _, _, err := svc.CreateWebhookEndpoint(ctx, owner, "http://127.0.0.1:9/hook"); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE webhook_endpoints SET status='disabled'`); err != nil {
		t.Fatalf("disable: %v", err)
	}
	if n, err := svc.FanoutWebhooks(ctx, "ledger.transfer.posted", []byte(`{}`)); err != nil || n != 0 {
		t.Fatalf("fanout n=%d err=%v want 0", n, err)
	}
}

func TestWebhooks_InvalidURL(t *testing.T) {
	pool := testhelpers.TestPool(t)
	testhelpers.AcquireAdvisoryLock(t, pool)
	testhelpers.TruncateAll(t, pool)
	svc := New(pool)
	owner := testhelpers.CreateUser(t, pool, "wh3-"+uuid.NewString()+"@test.local")
	for _, u := range []string{"", "ftp://x/y", "not-a-url", "http://"} {
		if _, _, err := svc.CreateWebhookEndpoint(context.Background(), owner, u); err == nil {
			t.Fatalf("url %q accepted", u)
		}
	}
}

func TestWebhooks_SignatureRoundTrip(t *testing.T) {
	body := []byte(`{"a":1}`)
	sig := SignWebhook("whsec_abc", body)
	if !VerifyWebhookSignature("whsec_abc", body, sig) {
		t.Fatalf("valid sig rejected")
	}
	if VerifyWebhookSignature("whsec_wrong", body, sig) {
		t.Fatalf("wrong secret accepted")
	}
	if VerifyWebhookSignature("whsec_abc", []byte(`{"a":2}`), sig) {
		t.Fatalf("tampered body accepted")
	}
}
