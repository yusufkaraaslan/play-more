package models

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"net"
	"net/url"
	"time"

	"github.com/google/uuid"
	"github.com/yusufkaraaslan/play-more/internal/storage"
)

// Webhook event names. These are the strings clients subscribe to
// via the `events` field on POST /api/v1/webhooks. New events
// MUST be added here AND to the dispatcher switch in
// internal/webhook/dispatcher.go.
const (
	WebhookEventGamePublished   = "game.published"
	WebhookEventGameUnpublished = "game.unpublished"
	WebhookEventBuildPromoted   = "build.promoted"
	WebhookEventBuildRolledBack = "build.rolled_back"
	WebhookEventReviewCreated   = "review.created"
	WebhookEventDevlogCreated   = "devlog.created"
	WebhookEventCommentCreated  = "comment.created"
)

// AllWebhookEvents returns the full set of valid event names.
// Used by the validator on POST/PUT /webhooks.
func AllWebhookEvents() []string {
	return []string{
		WebhookEventGamePublished,
		WebhookEventGameUnpublished,
		WebhookEventBuildPromoted,
		WebhookEventBuildRolledBack,
		WebhookEventReviewCreated,
		WebhookEventDevlogCreated,
		WebhookEventCommentCreated,
	}
}

// IsValidWebhookEvent reports whether name is one of the
// recognised event names. Unknown events are rejected at create
// time so a typo doesn't silently subscribe a user to nothing.
func IsValidWebhookEvent(name string) bool {
	for _, e := range AllWebhookEvents() {
		if e == name {
			return true
		}
	}
	return false
}

// Webhook is a per-user outbound event subscription. The
// `Secret` is shown to the user exactly once at creation; only
// the SHA-256 hash would be useful for verification but we keep
// the plaintext for signing outbound payloads — both are safe to
// store in the DB because the DB is in the user's own trust
// boundary (single-binary self-hosted app).
type Webhook struct {
	ID                  string   `json:"id"`
	UserID              string   `json:"user_id"`
	URL                 string   `json:"url"`
	Events              []string `json:"events"`
	Secret              string   `json:"secret,omitempty"` // only on create
	Active              bool     `json:"active"`
	ConsecutiveFailures int      `json:"consecutive_failures"`
	LastTriggeredAt     *string  `json:"last_triggered_at"`
	CreatedAt           string   `json:"created_at"`
}

// MaxWebhooksPerUser caps the number of active webhooks per
// account — a hard ceiling keeps an abusive user from saturating
// the dispatcher queue with thousands of URLs.
const MaxWebhooksPerUser = 20

// CreateWebhook inserts a new webhook. The secret is shown to
// the user exactly once via Webhook.Secret; the caller is
// responsible for surfacing it in the response. The secret is
// stored plaintext because we need it to sign outbound payloads;
// a hash-only storage would prevent signing.
func CreateWebhook(userID, url string, events []string) (*Webhook, error) {
	if len(events) == 0 {
		return nil, errWebhookNoEvents
	}
	for _, e := range events {
		if !IsValidWebhookEvent(e) {
			return nil, errWebhookInvalidEvent
		}
	}
	if err := ValidateWebhookURL(url); err != nil {
		return nil, err
	}

	// 32 random bytes → 64 hex chars. The secret is the
	// HMAC-SHA256 key for outbound deliveries.
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return nil, err
	}
	secret := hex.EncodeToString(b)
	eventsJSON, _ := json.Marshal(events)

	id := uuid.NewString()
	tx, err := storage.DB.Begin()
	if err != nil {
		return nil, err
	}
	var count int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM webhooks WHERE user_id = ?`, userID).Scan(&count); err != nil {
		tx.Rollback()
		return nil, err
	}
	if count >= MaxWebhooksPerUser {
		tx.Rollback()
		return nil, errWebhookLimitReached
	}
	if _, err := tx.Exec(
		`INSERT INTO webhooks (id, user_id, url, events, secret) VALUES (?, ?, ?, ?, ?)`,
		id, userID, url, string(eventsJSON), secret,
	); err != nil {
		tx.Rollback()
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &Webhook{
		ID:     id,
		UserID: userID,
		URL:    url,
		Events: events,
		Secret: secret,
		Active: true,
	}, nil
}

// Sentinel errors for CreateWebhook.
var (
	errWebhookNoEvents     = stringErr("webhook must subscribe to at least one event")
	errWebhookInvalidEvent = stringErr("unknown webhook event")
	errWebhookInvalidURL   = stringErr("webhook URL must be a valid http(s) URL")
	errWebhookLimitReached = stringErr("webhook limit reached")
	errWebhookURLBlocked   = stringErr("webhook URL resolves to a disallowed (private/loopback/link-local) address")
)

type stringErr string

func (e stringErr) Error() string { return string(e) }

// IsWebhookLimitError reports whether err is the per-user cap.
func IsWebhookLimitError(err error) bool { return err == errWebhookLimitReached }

// IsInvalidWebhookEventError reports whether err is the
// unknown-event sentinel. Used by the handler to return 400.
func IsInvalidWebhookEventError(err error) bool { return err == errWebhookInvalidEvent }

// IsWebhookNoEventsError reports whether err is the empty-events
// sentinel. Used by the handler to return 400 instead of 500.
func IsWebhookNoEventsError(err error) bool { return err == errWebhookNoEvents }

// IsWebhookURLError reports whether err is one of the URL
// validation failures (bad scheme/parse or blocked target). Used
// by the handler to return 400.
func IsWebhookURLError(err error) bool {
	return err == errWebhookInvalidURL || err == errWebhookURLBlocked
}

// AllowPrivateWebhookTargets, when true, disables the SSRF guard
// that blocks webhook delivery to loopback / private / link-local
// addresses. It exists ONLY so tests can point a webhook at an
// httptest server on 127.0.0.1. Never set true in production.
var AllowPrivateWebhookTargets = false

// ValidateWebhookURL enforces the webhook target policy: the URL
// must parse, use http or https, and (unless
// AllowPrivateWebhookTargets is set) must not resolve to a
// loopback, private, link-local, unique-local, unspecified, or
// multicast address. This is the SSRF guard for the single-binary
// self-hosted deploy — without it a user could register
// http://169.254.169.254/ (cloud metadata) or http://localhost/
// to reach services inside the trust boundary.
//
// Note: this validates at registration time. A hostile target can
// still rebind DNS between validation and delivery; a fully
// airtight guard would re-resolve and pin the IP at delivery time.
// Registration-time validation matches the documented follow-up
// and stops the common cases.
func ValidateWebhookURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return errWebhookInvalidURL
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return errWebhookInvalidURL
	}
	host := u.Hostname()
	if host == "" {
		return errWebhookInvalidURL
	}
	if AllowPrivateWebhookTargets {
		return nil
	}
	ips, err := net.LookupIP(host)
	if err != nil || len(ips) == 0 {
		// Can't resolve → can't safely deliver. Reject.
		return errWebhookURLBlocked
	}
	for _, ip := range ips {
		if isDisallowedTargetIP(ip) {
			return errWebhookURLBlocked
		}
	}
	return nil
}

// isDisallowedTargetIP reports whether ip is in a range a webhook
// must never be delivered to.
func isDisallowedTargetIP(ip net.IP) bool {
	return ip.IsLoopback() ||
		ip.IsPrivate() ||
		ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() ||
		ip.IsMulticast() ||
		ip.IsUnspecified()
}

// IsDisallowedWebhookTargetIP is the exported form of the SSRF IP
// policy, used by the dispatcher's dial-time guard so registration
// and delivery apply the exact same rule (defends DNS rebinding).
// It honours AllowPrivateWebhookTargets so tests may reach 127.0.0.1.
func IsDisallowedWebhookTargetIP(ip net.IP) bool {
	if AllowPrivateWebhookTargets {
		return false
	}
	return isDisallowedTargetIP(ip)
}

// ListWebhooks returns the user's webhooks. The Secret field
// is cleared — listing never reveals it.
func ListWebhooks(userID string) ([]*Webhook, error) {
	rows, err := storage.DB.Query(
		`SELECT id, user_id, url, events, active, consecutive_failures, last_triggered_at, created_at
		 FROM webhooks WHERE user_id = ? ORDER BY created_at DESC`,
		userID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Webhook
	for rows.Next() {
		w := &Webhook{}
		var eventsJSON string
		var lastTriggered sql.NullString
		if err := rows.Scan(&w.ID, &w.UserID, &w.URL, &eventsJSON, &w.Active, &w.ConsecutiveFailures, &lastTriggered, &w.CreatedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(eventsJSON), &w.Events)
		if lastTriggered.Valid {
			s := lastTriggered.String
			w.LastTriggeredAt = &s
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

// GetWebhook returns a single webhook by ID. Secret is NOT
// included — use GetWebhookWithSecret for signing.
func GetWebhook(webhookID, userID string) (*Webhook, error) {
	row := storage.DB.QueryRow(
		`SELECT id, user_id, url, events, active, consecutive_failures, last_triggered_at, created_at
		 FROM webhooks WHERE id = ? AND user_id = ?`,
		webhookID, userID,
	)
	w := &Webhook{}
	var eventsJSON string
	var lastTriggered sql.NullString
	if err := row.Scan(&w.ID, &w.UserID, &w.URL, &eventsJSON, &w.Active, &w.ConsecutiveFailures, &lastTriggered, &w.CreatedAt); err != nil {
		return nil, err
	}
	_ = json.Unmarshal([]byte(eventsJSON), &w.Events)
	if lastTriggered.Valid {
		s := lastTriggered.String
		w.LastTriggeredAt = &s
	}
	return w, nil
}

// GetWebhookWithSecret returns the webhook including its
// plaintext secret. The dispatcher is the only intended caller.
func GetWebhookWithSecret(webhookID string) (*Webhook, error) {
	row := storage.DB.QueryRow(
		`SELECT id, user_id, url, events, secret, active, consecutive_failures, last_triggered_at, created_at
		 FROM webhooks WHERE id = ?`,
		webhookID,
	)
	w := &Webhook{}
	var eventsJSON string
	var lastTriggered sql.NullString
	if err := row.Scan(&w.ID, &w.UserID, &w.URL, &eventsJSON, &w.Secret, &w.Active, &w.ConsecutiveFailures, &lastTriggered, &w.CreatedAt); err != nil {
		return nil, err
	}
	_ = json.Unmarshal([]byte(eventsJSON), &w.Events)
	if lastTriggered.Valid {
		s := lastTriggered.String
		w.LastTriggeredAt = &s
	}
	return w, nil
}

// ActiveWebhooksForEvent returns the active webhooks owned by
// userID that are subscribed to the given event. It is scoped to
// a single owner on purpose: an event fired by one developer must
// only ever be delivered to that same developer's webhooks —
// never cross-tenant. The dispatcher passes the event's OwnerID.
func ActiveWebhooksForEvent(userID, event string) ([]*Webhook, error) {
	rows, err := storage.DB.Query(
		`SELECT id, user_id, url, events, secret, active, consecutive_failures, last_triggered_at, created_at
		 FROM webhooks WHERE active = 1 AND user_id = ?`,
		userID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Webhook
	for rows.Next() {
		w := &Webhook{}
		var eventsJSON string
		var lastTriggered sql.NullString
		if err := rows.Scan(&w.ID, &w.UserID, &w.URL, &eventsJSON, &w.Secret, &w.Active, &w.ConsecutiveFailures, &lastTriggered, &w.CreatedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(eventsJSON), &w.Events)
		for _, e := range w.Events {
			if e == event {
				if lastTriggered.Valid {
					s := lastTriggered.String
					w.LastTriggeredAt = &s
				}
				out = append(out, w)
				break
			}
		}
	}
	return out, rows.Err()
}

// UpdateWebhook updates the URL, events, and active flag. The
// secret is intentionally NOT changeable here — rotating a
// secret means revoke + recreate, which is a security win (no
// race where the old secret is partially valid).
func UpdateWebhook(webhookID, userID, url string, events []string, active bool) error {
	if len(events) == 0 {
		return errWebhookNoEvents
	}
	for _, e := range events {
		if !IsValidWebhookEvent(e) {
			return errWebhookInvalidEvent
		}
	}
	if err := ValidateWebhookURL(url); err != nil {
		return err
	}
	eventsJSON, _ := json.Marshal(events)
	res, err := storage.DB.Exec(
		`UPDATE webhooks SET url = ?, events = ?, active = ? WHERE id = ? AND user_id = ?`,
		url, string(eventsJSON), boolToInt(active), webhookID, userID,
	)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// DeleteWebhook removes a webhook. CASCADE on the deliveries
// table purges history too.
func DeleteWebhook(webhookID, userID string) error {
	res, err := storage.DB.Exec(`DELETE FROM webhooks WHERE id = ? AND user_id = ?`, webhookID, userID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// RecordDelivery writes a row to webhook_deliveries.
func RecordDelivery(webhookID, event, payload string, attempt int, responseCode int, bodyExcerpt string, success bool) error {
	_, err := storage.DB.Exec(
		`INSERT INTO webhook_deliveries (webhook_id, event, payload, attempt, response_code, response_body_excerpt, success)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		webhookID, event, payload, attempt, responseCode, bodyExcerpt, boolToInt(success),
	)
	return err
}

// ListDeliveries returns the most recent N deliveries for a
// webhook. Used by the GET /webhooks/:id/deliveries endpoint.
func ListDeliveries(webhookID, userID string, limit int) ([]*Delivery, error) {
	// Authorization: only return deliveries for webhooks the user
	// owns. We do a separate check rather than a join so the limit
	// applies to deliveries, not to the (typically 1) webhook row.
	if _, err := GetWebhook(webhookID, userID); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := storage.DB.Query(
		`SELECT id, event, attempt, response_code, response_body_excerpt, delivered_at, success
		 FROM webhook_deliveries WHERE webhook_id = ?
		 ORDER BY delivered_at DESC LIMIT ?`,
		webhookID, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Delivery
	for rows.Next() {
		d := &Delivery{}
		var bodyExcerpt string
		var respCode sql.NullInt64
		if err := rows.Scan(&d.ID, &d.Event, &d.Attempt, &respCode, &bodyExcerpt, &d.DeliveredAt, &d.Success); err != nil {
			return nil, err
		}
		if respCode.Valid {
			n := int(respCode.Int64)
			d.ResponseCode = &n
		}
		d.ResponseBodyExcerpt = bodyExcerpt
		out = append(out, d)
	}
	return out, rows.Err()
}

// Delivery is one row of the webhook_deliveries log.
type Delivery struct {
	ID                  int64  `json:"id"`
	Event               string `json:"event"`
	Attempt             int    `json:"attempt"`
	ResponseCode        *int   `json:"response_code"`
	ResponseBodyExcerpt string `json:"response_body_excerpt"`
	DeliveredAt         string `json:"delivered_at"`
	Success             bool   `json:"success"`
}

// MarkTriggered bumps last_triggered_at and (on success) resets
// the consecutive_failures counter; on failure, increments it
// and disables the webhook past the threshold.
func MarkTriggered(webhookID string, success bool) error {
	if success {
		_, err := storage.DB.Exec(
			`UPDATE webhooks SET last_triggered_at = ?, consecutive_failures = 0 WHERE id = ?`,
			time.Now().UTC().Format(time.RFC3339), webhookID,
		)
		return err
	}
	_, err := storage.DB.Exec(
		`UPDATE webhooks
		 SET last_triggered_at = ?,
		     consecutive_failures = consecutive_failures + 1,
		     active = CASE WHEN consecutive_failures + 1 >= 10 THEN 0 ELSE active END
		 WHERE id = ?`,
		time.Now().UTC().Format(time.RFC3339), webhookID,
	)
	return err
}

// WebhookSignature returns the HMAC-SHA256 signature header
// value (e.g. "sha256=…") for the given payload + secret. The
// secret is the HMAC key. Used by the dispatcher at delivery time;
// the SDK verifies with crypto/hmac + hmac.Equal, so this must
// stay a standard HMAC-SHA256.
func WebhookSignature(secret string, payload []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
