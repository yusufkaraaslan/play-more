// Package webhook implements outbound event delivery for the
// webhooks feature. Hook points in handlers call Dispatch() to
// enqueue an event; a background worker fans the event out to
// all subscribed webhooks, signing each payload with HMAC-SHA256
// and retrying on transient failures.
//
// The dispatcher is intentionally simple: an in-process buffered
// channel + a single worker goroutine. A self-hosted single-binary
// app doesn't need Kafka — 10k events/hour is comfortable for a
// single worker on commodity hardware, and the user cap (20
// webhooks × ~10 events/min realistic) keeps the queue bounded.
package webhook

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/yusufkaraaslan/play-more/internal/models"
)

// Event is what the dispatcher carries in its queue. It is
// intentionally narrow — only the event name, the owning
// game/user id (for targeted lookups), and an opaque payload
// (handler-defined).
type Event struct {
	Name    string
	OwnerID string
	Payload map[string]any
}

// queue is a small buffered channel. If it ever fills, we drop
// events and log — the alternative (blocking the handler) is
// worse for a self-hosted app where a misconfigured webhook URL
// shouldn't take down the publishing path.
const queueDepth = 1024

var (
	queue   chan Event
	workers sync.WaitGroup
	cancel  context.CancelFunc
	mu      sync.Mutex
)

// Start launches the dispatcher worker. Safe to call multiple
// times across a process's lifetime — Stop followed by Start
// (e.g. between tests) restarts the worker cleanly. The worker
// exits when Stop is called.
func Start() {
	mu.Lock()
	defer mu.Unlock()
	if queue != nil {
		// Already running.
		return
	}
	q := make(chan Event, queueDepth)
	queue = q
	ctx, c := context.WithCancel(context.Background())
	cancel = c
	workers.Add(1)
	// The worker reads from its own channel handle, not the package
	// var, so Stop() can clear `queue` without racing the worker.
	go runWorker(ctx, q)
}

// Stop signals the worker to drain and exit. Safe to call when
// the worker is not running (it's a no-op in that case).
func Stop() {
	mu.Lock()
	if cancel == nil {
		mu.Unlock()
		return
	}
	cancel()
	cancel = nil
	queue = nil
	mu.Unlock()
	// Wait outside the lock: a drain can take tens of seconds (retry
	// backoffs), and holding mu that long would block Dispatch's
	// brief lock on the publishing path.
	workers.Wait()
}

// Dispatch enqueues an event for delivery. Non-blocking: if the
// queue is full, the event is dropped and a warning is logged.
// This is a deliberate trade-off — the publishing path should
// never block on outbound webhook delivery.
func Dispatch(name, ownerID string, payload map[string]any) {
	// Read the channel handle under the lock so we never race Stop()
	// setting queue = nil. The send itself is non-blocking (select
	// default), so we hold the lock only for the read.
	mu.Lock()
	q := queue
	mu.Unlock()
	if q == nil {
		// Dispatcher not started (e.g. in tests). Silently drop.
		return
	}
	e := Event{Name: name, OwnerID: ownerID, Payload: payload}
	select {
	case q <- e:
	default:
		log.Printf("webhook dispatcher: queue full, dropping event %s for %s", name, ownerID)
	}
}

func runWorker(ctx context.Context, q chan Event) {
	defer workers.Done()
	for {
		select {
		case <-ctx.Done():
			return
		case ev := <-q:
			deliverEvent(ctx, ev)
		}
	}
}

// deliverEvent fans an event out to all subscribed webhooks.
// Each delivery is independent — one slow URL doesn't block
// others. We use a small wait group so Stop() can wait for
// in-flight deliveries to finish.
func deliverEvent(ctx context.Context, ev Event) {
	// Scope the fan-out to the event owner's own webhooks. An event
	// fired by one developer must never be delivered to another
	// developer's subscription.
	hooks, err := models.ActiveWebhooksForEvent(ev.OwnerID, ev.Name)
	if err != nil {
		log.Printf("webhook: lookup for %s failed: %v", ev.Name, err)
		return
	}
	if len(hooks) == 0 {
		return
	}
	body, err := json.Marshal(map[string]any{
		"event":     ev.Name,
		"owner_id":  ev.OwnerID,
		"payload":   ev.Payload,
		"delivered": time.Now().UTC().Format(time.RFC3339),
	})
	if err != nil {
		log.Printf("webhook: marshal event %s: %v", ev.Name, err)
		return
	}
	var wg sync.WaitGroup
	for _, h := range hooks {
		wg.Add(1)
		go func(h *models.Webhook) {
			defer wg.Done()
			deliverOne(ctx, h, ev.Name, body)
		}(h)
	}
	wg.Wait()
}

// retrySchedule defines the per-attempt backoff. After the
// third attempt we give up — past that, the URL is almost
// certainly bad and we'd rather not pile up latency in the
// dispatcher.
var retrySchedule = []time.Duration{
	0,                // attempt 1: immediate
	5 * time.Second,  // attempt 2
	30 * time.Second, // attempt 3
}

// deliveryClient is shared across all deliveries so keep-alive
// connections are reused instead of re-doing a TCP+TLS handshake
// for every send to the same endpoint.
var deliveryClient = &http.Client{Timeout: 10 * time.Second}

func deliverOne(ctx context.Context, h *models.Webhook, eventName string, body []byte) {
	client := deliveryClient
	sig := models.WebhookSignature(h.Secret, body)
	for attempt, wait := range retrySchedule {
		if wait > 0 {
			select {
			case <-ctx.Done():
				return
			case <-time.After(wait):
			}
		}
		req, err := http.NewRequestWithContext(ctx, "POST", h.URL, bytes.NewReader(body))
		if err != nil {
			log.Printf("webhook %s: build request: %v", h.ID, err)
			return
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("User-Agent", "PlayMore-Webhook/1.0")
		req.Header.Set("X-PlayMore-Event", eventName)
		req.Header.Set("X-PlayMore-Signature", sig)
		req.Header.Set("X-PlayMore-Delivery", fmt.Sprintf("%s-%d", h.ID, time.Now().UnixNano()))
		req.Header.Set("X-PlayMore-Attempt", fmt.Sprintf("%d", attempt+1))

		resp, err := client.Do(req)
		if err != nil {
			// If we're shutting down (ctx cancelled), this isn't the
			// target's fault — don't record it as a failure or it
			// would push a healthy webhook toward the auto-disable
			// threshold across restarts.
			if ctx.Err() != nil {
				return
			}
			// Network error — retry until we run out of attempts.
			_ = models.RecordDelivery(h.ID, eventName, string(body), attempt+1, 0, err.Error(), false)
			continue
		}
		// Drain the body (cap to 512 bytes) so the connection can be reused.
		bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		resp.Body.Close()
		excerpt := string(bodyBytes)
		success := resp.StatusCode >= 200 && resp.StatusCode < 300
		_ = models.RecordDelivery(h.ID, eventName, string(body), attempt+1, resp.StatusCode, excerpt, success)
		if success {
			_ = models.MarkTriggered(h.ID, true)
			return
		}
		// 4xx is non-retryable (the URL is bad). 5xx is retryable.
		if resp.StatusCode >= 400 && resp.StatusCode < 500 {
			_ = models.MarkTriggered(h.ID, false)
			return
		}
	}
	// All attempts failed (only 5xx path lands here).
	_ = models.MarkTriggered(h.ID, false)
}
