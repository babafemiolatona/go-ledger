package ledger

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	WebhookMaxAttempts = 8
	WebhookHTTPTimeout = 10 * time.Second

	webhookBackoffBase = 5 * time.Second
	webhookBackoffCap  = time.Hour
	webhookBatchSize   = 10
)

func WebhookBackoff(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	d := webhookBackoffBase
	for i := 1; i < attempt; i++ {
		d *= 2
		if d >= webhookBackoffCap {
			return webhookBackoffCap
		}
	}
	if d > webhookBackoffCap {
		return webhookBackoffCap
	}
	return d
}

func SignWebhook(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func VerifyWebhookSignature(secret string, body []byte, sig string) bool {
	want := SignWebhook(secret, body)
	return hmac.Equal([]byte(strings.TrimSpace(sig)), []byte(want))
}

type WebhookEndpoint struct {
	ID        uuid.UUID `json:"id"`
	OwnerID   uuid.UUID `json:"owner_id"`
	URL       string    `json:"url"`
	Status    string    `json:"status"`
	CreatedAt string    `json:"created_at"`
}

type WebhookDelivery struct {
	ID          int64     `json:"id"`
	EndpointID  uuid.UUID `json:"endpoint_id"`
	Topic       string    `json:"topic"`
	Payload     string    `json:"payload"`
	Attempts    int       `json:"attempts"`
	LastStatus  *int      `json:"last_status,omitempty"`
	LastError   *string   `json:"last_error,omitempty"`
	NextRetryAt string    `json:"next_retry_at"`
	DeliveredAt *string   `json:"delivered_at,omitempty"`
	Status      string    `json:"status"`
	CreatedAt   string    `json:"created_at"`
}

func generateWebhookSecret() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "whsec_" + hex.EncodeToString(b), nil
}

func (s *Service) CreateWebhookEndpoint(ctx context.Context, ownerID uuid.UUID, rawURL string) (WebhookEndpoint, string, error) {
	rawURL = strings.TrimSpace(rawURL)
	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return WebhookEndpoint{}, "", fmt.Errorf("%w: url must be http(s) with a host", ErrValidation)
	}
	secret, err := generateWebhookSecret()
	if err != nil {
		return WebhookEndpoint{}, "", err
	}
	var ep WebhookEndpoint
	var created string
	err = s.pool.QueryRow(ctx,
		`INSERT INTO webhook_endpoints (owner_id, url, secret) VALUES ($1,$2,$3)
		 RETURNING id, owner_id, url, status, created_at::text`,
		ownerID, rawURL, secret).Scan(&ep.ID, &ep.OwnerID, &ep.URL, &ep.Status, &created)
	if err != nil {
		return WebhookEndpoint{}, "", err
	}
	ep.CreatedAt = created
	return ep, secret, nil
}

func (s *Service) ListWebhookEndpoints(ctx context.Context, ownerID uuid.UUID, all bool) ([]WebhookEndpoint, error) {
	q := `SELECT id, owner_id, url, status, created_at::text FROM webhook_endpoints WHERE owner_id=$1 ORDER BY created_at`
	args := []any{ownerID}
	if all {
		q = `SELECT id, owner_id, url, status, created_at::text FROM webhook_endpoints ORDER BY created_at`
		args = nil
	}
	r, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	var out []WebhookEndpoint
	for r.Next() {
		var ep WebhookEndpoint
		var created string
		if err := r.Scan(&ep.ID, &ep.OwnerID, &ep.URL, &ep.Status, &created); err != nil {
			return nil, err
		}
		ep.CreatedAt = created
		out = append(out, ep)
	}
	return out, r.Err()
}

func (s *Service) GetWebhookEndpointOwner(ctx context.Context, endpointID uuid.UUID) (uuid.UUID, error) {
	var ownerID uuid.UUID
	err := s.pool.QueryRow(ctx, `SELECT owner_id FROM webhook_endpoints WHERE id=$1`, endpointID).Scan(&ownerID)
	if err != nil {
		return uuid.Nil, err
	}
	return ownerID, nil
}

func (s *Service) ListWebhookDeliveries(ctx context.Context, ownerID uuid.UUID, all bool, endpointID *uuid.UUID, status string) ([]WebhookDelivery, error) {
	q := `SELECT d.id, d.endpoint_id, d.topic, d.payload::text, d.attempts,
	        d.last_status, d.last_error, d.next_retry_at::text, d.delivered_at::text,
	        d.status, d.created_at::text
	      FROM webhook_deliveries d JOIN webhook_endpoints e ON e.id=d.endpoint_id
	      WHERE ($1 OR e.owner_id=$2)`
	args := []any{all, ownerID}
	if endpointID != nil {
		q += ` AND d.endpoint_id=$3`
		args = append(args, *endpointID)
	}
	if status != "" {
		if status != "queued" && status != "delivered" && status != "dead" {
			return nil, fmt.Errorf("%w: invalid status filter", ErrValidation)
		}
		q += fmt.Sprintf(` AND d.status=$%d`, len(args)+1)
		args = append(args, status)
	}
	q += ` ORDER BY d.id DESC LIMIT 100`
	r, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	var out []WebhookDelivery
	for r.Next() {
		var d WebhookDelivery
		var payload string
		if err := r.Scan(&d.ID, &d.EndpointID, &d.Topic, &payload, &d.Attempts,
			&d.LastStatus, &d.LastError, &d.NextRetryAt, &d.DeliveredAt, &d.Status, &d.CreatedAt); err != nil {
			return nil, err
		}
		d.Payload = payload
		out = append(out, d)
	}
	return out, r.Err()
}

func (s *Service) FanoutWebhooks(ctx context.Context, topic string, payload []byte) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx,
		`WITH ins AS (
		   INSERT INTO webhook_deliveries (endpoint_id, topic, payload)
		   SELECT id, $1, $2::jsonb FROM webhook_endpoints WHERE status='active'
		   RETURNING id
		 ) SELECT count(*) FROM ins`, topic, string(payload)).Scan(&n)
	if err != nil {
		return 0, err
	}
	return n, nil
}

func (s *Service) DeliverDueWebhooks(ctx context.Context, client *http.Client) (int, error) {
	type due struct {
		id         int64
		endpointID uuid.UUID
		topic      string
		payload    []byte
		attempts   int
		url        string
		secret     string
	}
	r, err := s.pool.Query(ctx,
		`SELECT d.id, d.endpoint_id, d.topic, d.payload::text, d.attempts, e.url, e.secret
		 FROM webhook_deliveries d JOIN webhook_endpoints e ON e.id=d.endpoint_id
		 WHERE d.status='queued' AND d.next_retry_at <= now() AND e.status='active'
		 ORDER BY d.id LIMIT $1 FOR UPDATE OF d SKIP LOCKED`, webhookBatchSize)
	if err != nil {
		return 0, err
	}
	var jobs []due
	for r.Next() {
		var j due
		var payload string
		if err := r.Scan(&j.id, &j.endpointID, &j.topic, &payload, &j.attempts, &j.url, &j.secret); err != nil {
			r.Close()
			return 0, err
		}
		j.payload = []byte(payload)
		jobs = append(jobs, j)
	}
	r.Close()
	if err := r.Err(); err != nil {
		return 0, err
	}

	delivered := 0
	for _, j := range jobs {
		status, derr := postWebhook(ctx, client, j.url, j.secret, j.id, j.topic, j.payload, j.attempts+1)
		if derr == nil && status >= 200 && status < 300 {
			if _, err := s.pool.Exec(ctx,
				`UPDATE webhook_deliveries SET status='delivered', delivered_at=now(),
				 attempts=attempts+1, last_status=$2 WHERE id=$1`,
				j.id, status); err != nil {
				return delivered, err
			}
			delivered++
			continue
		}
		next := j.attempts + 1
		var msg string
		if derr != nil {
			msg = derr.Error()
		} else {
			msg = "unexpected status " + strconv.Itoa(status)
		}
		if len(msg) > 500 {
			msg = msg[:500]
		}
		if next >= WebhookMaxAttempts {
			if _, err := s.pool.Exec(ctx,
				`UPDATE webhook_deliveries SET status='dead', attempts=$2, last_status=$3,
				 last_error=$4 WHERE id=$1`,
				j.id, next, nullableStatus(status, derr), msg); err != nil {
				return delivered, err
			}
			continue
		}
		if _, err := s.pool.Exec(ctx,
			`UPDATE webhook_deliveries SET attempts=$2, last_status=$3, last_error=$4,
			 next_retry_at=now()+($5::text::interval) WHERE id=$1`,
			j.id, next, nullableStatus(status, derr), msg, backoffInterval(next)); err != nil {
			return delivered, err
		}
	}
	return delivered, nil
}

func nullableStatus(status int, derr error) *int {
	if derr != nil {
		return nil
	}
	return &status
}

func backoffInterval(attempt int) string {
	return strconv.FormatInt(int64(WebhookBackoff(attempt)/time.Second), 10) + " seconds"
}

func postWebhook(ctx context.Context, client *http.Client, rawURL, secret string, deliveryID int64, topic string, payload []byte, attempt int) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, rawURL, bytes.NewReader(payload))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Webhook-Topic", topic)
	req.Header.Set("X-Webhook-Delivery", strconv.FormatInt(deliveryID, 10))
	req.Header.Set("X-Webhook-Attempt", strconv.Itoa(attempt))
	req.Header.Set("X-Webhook-Signature", SignWebhook(secret, payload))
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64*1024))
	return resp.StatusCode, nil
}
