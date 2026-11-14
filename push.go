package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	firebase "firebase.google.com/go/v4"
	"firebase.google.com/go/v4/messaging"
	webpush "github.com/SherClockHolmes/webpush-go"
	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"google.golang.org/api/option"
)

type pushDelivery struct {
	fcm                      *messaging.Client
	public, private, contact string
}

func (p *pushDelivery) init(ctx context.Context) error {
	p.public = os.Getenv("VAPID_PUBLIC_KEY")
	p.private = os.Getenv("VAPID_PRIVATE_KEY")
	p.contact = os.Getenv("VAPID_CONTACT")
	if (p.public != "" || p.private != "") && (p.public == "" || p.private == "" || p.contact == "") {
		return errInvalid
	}
	if file := os.Getenv("FCM_CREDENTIALS_FILE"); file != "" {
		app, err := firebase.NewApp(ctx, nil, option.WithCredentialsFile(file))
		if err != nil {
			return err
		}
		p.fcm, err = app.Messaging(ctx)
		return err
	}
	return nil
}

func (s *service) pushInfo(e *core.RequestEvent) error {
	return e.JSON(200, map[string]any{"android": s.push.fcm != nil, "webVapidPublicKey": s.push.public})
}

func allowedPushURL(value string) bool {
	u, err := url.Parse(value)
	if err != nil || u.Scheme != "https" || u.User != nil || (u.Port() != "" && u.Port() != "443") || u.Fragment != "" {
		return false
	}
	host := strings.ToLower(u.Hostname())
	return host == "fcm.googleapis.com" || host == "updates.push.services.mozilla.com" ||
		host == "web.push.apple.com" || strings.HasSuffix(host, ".push.apple.com") || strings.HasSuffix(host, ".notify.windows.com")
}

func (s *service) registerDevice(e *core.RequestEvent) error {
	var body struct {
		Platform string `json:"platform"`
		Token    string `json:"token"`
		Endpoint string `json:"endpoint"`
		P256DH   string `json:"p256dh"`
		Auth     string `json:"auth"`
	}
	if e.BindBody(&body) != nil {
		return apiError(e, errInvalid)
	}
	dest := body.Token
	switch body.Platform {
	case "android":
		if dest == "" || len(dest) > 4096 {
			return apiError(e, errInvalid)
		}
	case "web":
		dest = body.Endpoint
		if len(dest) > 4096 || !allowedPushURL(dest) || body.P256DH == "" || body.Auth == "" || len(body.P256DH) > 150 || len(body.Auth) > 100 {
			return apiError(e, errInvalid)
		}
	default:
		return apiError(e, errInvalid)
	}
	var device *core.Record
	err := s.app.RunInTransaction(func(tx core.App) error {
		var err error
		device, err = tx.FindFirstRecordByFilter("devices", "platform = {:p} && destination = {:d}", dbx.Params{"p": body.Platform, "d": dest})
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if device != nil && device.GetString("user") != e.Auth.Id {
			return errConflict
		}
		if device == nil {
			devices, err := tx.FindRecordsByFilter("devices", "user = {:u}", "", 10, 0, dbx.Params{"u": e.Auth.Id})
			if err != nil {
				return err
			}
			if len(devices) >= 10 {
				return errConflict
			}
			device, err = newRow(tx, "devices")
			if err != nil {
				return err
			}
		}
		device.Set("platform", body.Platform)
		device.Set("destination", dest)
		device.Set("user", e.Auth.Id)
		device.Set("p256dh", body.P256DH)
		device.Set("auth_secret", body.Auth)
		return tx.Save(device)
	})
	if err != nil {
		return apiError(e, err)
	}
	return e.JSON(200, device)
}

func (s *service) removeDevice(e *core.RequestEvent) error {
	err := s.app.RunInTransaction(func(tx core.App) error {
		device, err := tx.FindRecordById("devices", e.Request.PathValue("id"))
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if device.GetString("user") != e.Auth.Id {
			return errMissing
		}
		return deleteDevice(tx, device)
	})
	if err != nil {
		return apiError(e, err)
	}
	return e.NoContent(204)
}

func deleteDevice(app core.App, device *core.Record) error {
	jobs, err := app.FindRecordsByFilter("notification_outbox", "device = {:d}", "", 0, 0, dbx.Params{"d": device.Id})
	if err != nil {
		return err
	}
	for _, job := range jobs {
		if err := app.Delete(job); err != nil {
			return err
		}
	}
	return app.Delete(device)
}

func enqueue(app core.App, user, event, ref string, expires time.Time) error {
	devices, err := app.FindRecordsByFilter("devices", "user = {:u}", "", 0, 0, dbx.Params{"u": user})
	if err != nil {
		return err
	}
	for _, device := range devices {
		job, err := newRow(app, "notification_outbox")
		if err != nil {
			return err
		}
		job.Set("user", user)
		job.Set("device", device.Id)
		job.Set("event", event)
		job.Set("reference", ref)
		job.Set("expires_at", expires)
		job.Set("next_attempt", time.Now())
		if err = app.Save(job); err != nil {
			return err
		}
	}
	return nil
}

func (s *service) processOutbox(ctx context.Context) {
	if err := s.app.RunInTransaction(expireCalls); err != nil {
		s.app.Logger().Error("Call expiry failed", "error", err)
	}
	jobs, err := s.app.FindRecordsByFilter("notification_outbox", "next_attempt <= {:now} || expires_at <= {:now}", "next_attempt", 20, 0, dbx.Params{"now": dbTime(time.Now())})
	if err != nil {
		s.app.Logger().Error("Push queue read failed", "error", err)
		return
	}
	for _, job := range jobs {
		if ctx.Err() != nil {
			return
		}
		s.deliverJob(ctx, job)
	}
}

func (s *service) deliverJob(ctx context.Context, job *core.Record) {
	expire := job.GetDateTime("expires_at").Time()
	event, ref := job.GetString("event"), job.GetString("reference")
	if event == "incoming_call" {
		call, err := s.app.FindRecordById("calls", ref)
		if err != nil || call.GetString("status") != "ringing" {
			expire = time.Now()
		}
	}
	if !time.Now().Before(expire) {
		_ = s.app.Delete(job)
		return
	}
	device, err := s.app.FindRecordById("devices", job.GetString("device"))
	if err != nil || device.GetString("user") != job.GetString("user") {
		_ = s.app.Delete(job)
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	remove, err := s.push.deliver(ctx, device, event, ref, expire)
	if remove {
		_ = s.app.RunInTransaction(func(tx core.App) error { return deleteDevice(tx, device) })
		return
	}
	if err == nil {
		_ = s.app.Delete(job)
		return
	}
	attempts := job.GetInt("attempts") + 1
	if attempts >= 5 {
		s.app.Logger().Warn("Push delivery exhausted", "event", event, "device", device.Id)
		_ = s.app.Delete(job)
		return
	}
	job.Set("attempts", attempts)
	job.Set("next_attempt", time.Now().Add(time.Duration(1<<attempts)*time.Second))
	_ = s.app.Save(job)
}

var pushHTTP = &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

func (p *pushDelivery) deliver(ctx context.Context, d *core.Record, event, ref string, expires time.Time) (bool, error) {
	payload := map[string]string{"event": event, "id": ref, "expiresAt": expires.UTC().Format(time.RFC3339Nano)}
	switch d.GetString("platform") {
	case "android":
		if p.fcm == nil {
			return false, errors.New("FCM unavailable")
		}
		ttl := max(time.Second, time.Until(expires))
		priority := "normal"
		if event == "incoming_call" {
			priority = "high"
		}
		_, err := p.fcm.Send(ctx, &messaging.Message{
			Token: d.GetString("destination"), Data: payload, Android: &messaging.AndroidConfig{Priority: priority, TTL: &ttl},
		})
		return messaging.IsUnregistered(err), err
	case "web":
		if p.public == "" {
			return false, errors.New("Web Push unavailable")
		}
		if !allowedPushURL(d.GetString("destination")) {
			return true, errInvalid
		}
		data, err := json.Marshal(payload)
		if err != nil {
			return false, err
		}
		res, err := webpush.SendNotificationWithContext(ctx, data, &webpush.Subscription{
			Endpoint: d.GetString("destination"), Keys: webpush.Keys{P256dh: d.GetString("p256dh"), Auth: d.GetString("auth_secret")},
		}, &webpush.Options{HTTPClient: pushHTTP, Subscriber: p.contact, VAPIDPublicKey: p.public, VAPIDPrivateKey: p.private, TTL: max(1, int(time.Until(expires).Seconds()))})
		if err != nil {
			return false, err
		}
		defer res.Body.Close()
		if res.StatusCode == 404 || res.StatusCode == 410 {
			return true, nil
		}
		if res.StatusCode >= 200 && res.StatusCode < 300 {
			return false, nil
		}
		return false, errors.New("Web Push delivery rejected")
	default:
		return true, errInvalid
	}
}
