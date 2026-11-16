package main

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"slices"
	"time"

	"github.com/livekit/protocol/auth"
	"github.com/livekit/protocol/livekit"
	"github.com/livekit/protocol/webhook"
	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
	"github.com/pocketbase/pocketbase/tools/security"
)

const ringTime = 45 * time.Second
const tokenLife = 5 * time.Minute

func init() { m.Register(secureCallSchema, nil, "1794900000_secure_call.go") }

func secureCallSchema(app core.App) error {
	calls, err := app.FindCollectionByNameOrId("calls")
	if err != nil {
		return err
	}
	calls.Fields.Add(&core.TextField{Name: "secure_by", Max: 15})
	return app.Save(calls)
}

func (s *service) secureCall(e *core.RequestEvent) error {
	var body struct {
		Enabled bool `json:"enabled"`
	}
	if err := e.BindBody(&body); err != nil {
		return apiError(e, errInvalid)
	}
	var call *core.Record
	err := s.app.RunInTransaction(func(tx core.App) error {
		var err error
		call, err = visibleCall(tx, e.Request.PathValue("id"), e.Auth.Id)
		if err != nil {
			return err
		}
		if status := call.GetString("status"); status != "accepted" && status != "connected" {
			return errConflict
		}
		owner := call.GetString("secure_by")
		if body.Enabled {
			if owner != "" && owner != e.Auth.Id {
				return errConflict
			}
			call.Set("secure_by", e.Auth.Id)
		} else {
			if owner != "" && owner != e.Auth.Id {
				return errConflict
			}
			call.Set("secure_by", "")
		}
		return tx.Save(call)
	})
	if err != nil {
		return apiError(e, err)
	}
	return e.JSON(200, call)
}

var errMissing = errors.New("record unavailable")
var errConflict = errors.New("state conflict")
var errInvalid = errors.New("invalid request")
var errQuota = errors.New("attachment storage limit reached")

func apiError(e *core.RequestEvent, err error) error {
	switch {
	case errors.Is(err, errMissing):
		return e.NotFoundError("Record unavailable", nil)
	case errors.Is(err, errConflict):
		return e.JSON(409, map[string]string{"message": "Action no longer available"})
	case errors.Is(err, errInvalid):
		return e.BadRequestError("Invalid request", nil)
	case errors.Is(err, errQuota):
		return e.JSON(507, map[string]string{"message": errQuota.Error()})
	default:
		e.App.Logger().Error("Request failed", "error", err)
		return e.InternalServerError("Request failed", nil)
	}
}

func newRow(app core.App, collection string) (*core.Record, error) {
	c, err := app.FindCollectionByNameOrId(collection)
	if err != nil {
		return nil, err
	}
	return core.NewRecord(c), nil
}

func memberRecord(app core.App, conv, user string) (*core.Record, error) {
	r, err := app.FindRecordById("conversations", conv)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, errMissing
	}
	if err != nil {
		return nil, err
	}
	if !slices.Contains(r.GetStringSlice("members"), user) {
		return nil, errMissing
	}
	return r, nil
}

func visibleCall(app core.App, id, user string) (*core.Record, error) {
	call, err := app.FindRecordById("calls", id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, errMissing
	}
	if err != nil {
		return nil, err
	}
	if user != call.GetString("caller") && user != call.GetString("receiver") {
		return nil, errMissing
	}
	return call, nil
}

func (s *service) startCall(e *core.RequestEvent) error {
	if s.room == nil {
		return e.JSON(503, map[string]string{"message": "Calling is unavailable"})
	}
	var body struct {
		Conversation string `json:"conversation"`
		Kind         string `json:"kind"`
	}
	if e.BindBody(&body) != nil || (body.Kind != "audio" && body.Kind != "video") {
		return apiError(e, errInvalid)
	}
	var call *core.Record
	err := s.app.RunInTransaction(func(tx core.App) error {
		conv, err := memberRecord(tx, body.Conversation, e.Auth.Id)
		if err != nil {
			return err
		}
		members := conv.GetStringSlice("members")
		receiver := members[0]
		if receiver == e.Auth.Id {
			receiver = members[1]
		}
		if err := expireCalls(tx); err != nil {
			return err
		}
		_, err = tx.FindFirstRecordByFilter("calls", "(status = 'ringing' || status = 'accepted' || status = 'connected') && (caller = {:a} || receiver = {:a} || caller = {:b} || receiver = {:b})", dbx.Params{"a": e.Auth.Id, "b": receiver})
		if err == nil {
			return errConflict
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		call, err = newRow(tx, "calls")
		if err != nil {
			return err
		}
		expires := time.Now().Add(ringTime)
		call.Set("conversation", conv.Id)
		call.Set("caller", e.Auth.Id)
		call.Set("caller_session", requestSession(e).Id)
		call.Set("receiver", receiver)
		call.Set("room", "call-"+security.RandomString(32))
		call.Set("kind", body.Kind)
		call.Set("status", "ringing")
		call.Set("ring_expires_at", expires)
		if err := tx.Save(call); err != nil {
			return err
		}
		return enqueue(tx, receiver, "incoming_call", call.Id, expires)
	})
	if err != nil {
		return apiError(e, err)
	}
	return e.JSON(201, call)
}

func (s *service) changeCall(action string) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		var body struct {
			DeviceID string `json:"deviceId"`
		}
		if action == "accept" && (e.BindBody(&body) != nil || body.DeviceID == "") {
			return apiError(e, errInvalid)
		}
		var call *core.Record
		err := s.app.RunInTransaction(func(tx core.App) error {
			var err error
			call, err = visibleCall(tx, e.Request.PathValue("id"), e.Auth.Id)
			if err != nil {
				return err
			}
			status := call.GetString("status")
			receiver := e.Auth.Id == call.GetString("receiver")
			now := time.Now()
			switch action {
			case "accept":
				if !receiver {
					return errMissing
				}
				if (status == "accepted" || status == "connected") && requestSession(e).Id == call.GetString("receiver_session") {
					return nil
				}
				if status != "ringing" || !now.Before(call.GetDateTime("ring_expires_at").Time()) {
					return errConflict
				}
				if body.DeviceID != requestSession(e).GetString("device") {
					return errInvalid
				}
				call.Set("accepted_device", body.DeviceID)
				call.Set("receiver_session", requestSession(e).Id)
				call.Set("status", "accepted")
				call.Set("accepted_at", now)
			case "reject":
				if !receiver {
					return errMissing
				}
				if status == "rejected" {
					return nil
				}
				if status != "ringing" || !now.Before(call.GetDateTime("ring_expires_at").Time()) {
					return errConflict
				}
				finish(call, "rejected")
			case "end":
				switch status {
				case "ringing":
					if receiver {
						return errConflict
					}
					finish(call, "cancelled")
				case "accepted", "connected":
					finish(call, "ended")
				default:
					return nil
				}
			}
			if err := tx.Save(call); err != nil {
				return err
			}
			if action != "accept" {
				for _, user := range []string{call.GetString("caller"), call.GetString("receiver")} {
					if err := enqueue(tx, user, "call_ended", call.Id, time.Now().Add(2*time.Minute)); err != nil {
						return err
					}
				}
			}
			return nil
		})
		if err != nil {
			return apiError(e, err)
		}
		if action == "end" && call.GetBool("cleanup_pending") {
			s.deleteRoom(e.Request.Context(), call)
		}
		return e.JSON(200, call)
	}
}

func finish(call *core.Record, status string) {
	old := call.GetString("status")
	call.Set("status", status)
	call.Set("ended_at", time.Now())
	if old == "accepted" || old == "connected" {
		call.Set("cleanup_pending", true)
	}
}

func (s *service) callToken(e *core.RequestEvent) error {
	if s.room == nil {
		return e.JSON(503, map[string]string{"message": "Calling is unavailable"})
	}
	var body struct {
		DeviceID string `json:"deviceId"`
	}
	if e.BindBody(&body) != nil || body.DeviceID == "" {
		return apiError(e, errInvalid)
	}
	call, err := visibleCall(s.app, e.Request.PathValue("id"), e.Auth.Id)
	if err != nil {
		return apiError(e, err)
	}
	if status := call.GetString("status"); status != "accepted" && status != "connected" {
		return apiError(e, errConflict)
	}
	session := requestSession(e)
	expectedSession := call.GetString("caller_session")
	if e.Auth.Id == call.GetString("receiver") {
		expectedSession = call.GetString("receiver_session")
	}
	if expectedSession != session.Id || body.DeviceID != session.GetString("device") {
		return apiError(e, errConflict)
	}
	room := call.GetString("room")
	ctx, cancel := context.WithTimeout(e.Request.Context(), 6*time.Second)
	defer cancel()
	if _, err := s.room.CreateRoom(ctx, &livekit.CreateRoomRequest{Name: room, MaxParticipants: 2, EmptyTimeout: 90, DepartureTimeout: 60}); err != nil {
		return apiError(e, err)
	}
	latest, err := visibleCall(s.app, call.Id, e.Auth.Id)
	if err != nil {
		return apiError(e, err)
	}
	if status := latest.GetString("status"); status != "accepted" && status != "connected" {
		s.deleteRoom(ctx, latest)
		return apiError(e, errConflict)
	}
	grant := &auth.VideoGrant{RoomJoin: true, Room: room}
	grant.SetCanPublish(true)
	grant.SetCanSubscribe(true)
	grant.SetCanPublishData(false)
	token, err := auth.NewAccessToken(s.key, s.secret).SetIdentity(e.Auth.Id).SetName(e.Auth.GetString("name")).
		SetValidFor(tokenLife).SetVideoGrant(grant).ToJWT()
	if err != nil {
		return apiError(e, err)
	}
	return e.JSON(200, map[string]any{"token": token, "roomName": room, "wsUrl": s.wsURL, "expiresAt": time.Now().Add(tokenLife)})
}

func (s *service) livekitWebhook(e *core.RequestEvent) error {
	if s.room == nil {
		return e.NotFoundError("Unavailable", nil)
	}
	e.Request.Body = http.MaxBytesReader(e.Response, e.Request.Body, 1<<20)
	event, err := webhook.ReceiveWebhookEvent(e.Request, auth.NewSimpleKeyProvider(s.key, s.secret))
	if err != nil {
		return e.UnauthorizedError("Invalid event", nil)
	}
	room := event.GetRoom().GetName()
	switch event.GetEvent() {
	case "participant_joined", "participant_left", "room_finished":
		if err := s.reconcileRoom(e.Request.Context(), room, event.GetEvent() == "room_finished"); err != nil {
			return apiError(e, err)
		}
	}
	return e.NoContent(204)
}

func (s *service) reconcileRoom(ctx context.Context, room string, finished bool) error {
	call, err := s.app.FindFirstRecordByData("calls", "room", room)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	status := call.GetString("status")
	if status != "accepted" && status != "connected" {
		if status != "ringing" && !finished {
			s.deleteRoom(ctx, call)
		}
		return nil
	}
	count := 0
	if !finished {
		ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
		defer cancel()
		list, err := s.room.ListParticipants(ctx, &livekit.ListParticipantsRequest{Room: room})
		if err != nil && !roomMissing(err) {
			return err
		}
		for _, p := range list.GetParticipants() {
			if p.Identity == call.GetString("caller") || p.Identity == call.GetString("receiver") {
				count++
			}
		}
	}
	return s.app.RunInTransaction(func(tx core.App) error {
		call, err := tx.FindRecordById("calls", call.Id)
		if err != nil {
			return err
		}
		status := call.GetString("status")
		if status != "accepted" && status != "connected" {
			return nil
		}
		now := time.Now()
		switch {
		case status == "accepted" && count == 2:
			call.Set("status", "connected")
			call.Set("connected_at", now)
		case status == "accepted" && now.Sub(call.GetDateTime("accepted_at").Time()) > time.Minute:
			finish(call, "failed")
		case status == "connected" && finished:
			finish(call, "ended")
		case status == "connected" && count < 2:
			since := call.GetDateTime("empty_since")
			if since.IsZero() {
				call.Set("empty_since", now)
			} else if now.Sub(since.Time()) >= 45*time.Second {
				finish(call, "ended")
			}
		case count == 2 && !call.GetDateTime("empty_since").IsZero():
			call.Set("empty_since", "")
		default:
			return nil
		}
		return tx.Save(call)
	})
}

func roomMissing(err error) bool {
	var coded interface{ Code() string }
	return errors.As(err, &coded) && coded.Code() == "not_found"
}

func (s *service) deleteRoom(ctx context.Context, call *core.Record) {
	if s.room == nil {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	_, err := s.room.DeleteRoom(ctx, &livekit.DeleteRoomRequest{Room: call.GetString("room")})
	if err != nil && !roomMissing(err) {
		s.app.Logger().Warn("Room cleanup deferred", "call", call.Id)
		return
	}
	_ = s.app.RunInTransaction(func(tx core.App) error {
		row, err := tx.FindRecordById("calls", call.Id)
		if err != nil {
			return err
		}
		if !row.GetBool("cleanup_pending") {
			return nil
		}
		row.Set("cleanup_pending", false)
		return tx.Save(row)
	})
}

func expireCalls(app core.App) error {
	calls, err := app.FindRecordsByFilter("calls", "status = 'ringing' && ring_expires_at <= {:now}", "", 0, 0, dbx.Params{"now": dbTime(time.Now())})
	if err != nil {
		return err
	}
	for _, call := range calls {
		finish(call, "missed")
		if err := app.Save(call); err != nil {
			return err
		}
		for _, user := range []string{call.GetString("caller"), call.GetString("receiver")} {
			if err := enqueue(app, user, "call_ended", call.Id, time.Now().Add(2*time.Minute)); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *service) maintain() {
	if err := s.app.RunInTransaction(expireCalls); err != nil {
		s.app.Logger().Error("Call expiry failed", "error", err)
	}
	if s.room == nil {
		return
	}
	calls, err := s.app.FindRecordsByFilter("calls", "status = 'accepted' || status = 'connected' || cleanup_pending = true", "", 0, 0)
	if err != nil {
		s.app.Logger().Error("Call reconciliation failed", "error", err)
		return
	}
	for _, call := range calls {
		if call.GetBool("cleanup_pending") {
			s.deleteRoom(context.Background(), call)
			continue
		}
		if err := s.reconcileRoom(context.Background(), call.GetString("room"), false); err != nil {
			s.app.Logger().Warn("Room reconciliation deferred", "call", call.Id)
		}
	}
}

func dbTime(t time.Time) string { return t.UTC().Format("2006-01-02 15:04:05.000Z") }
