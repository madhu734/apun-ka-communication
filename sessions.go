package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"time"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
	"github.com/pocketbase/pocketbase/tools/hook"
	"github.com/pocketbase/pocketbase/tools/security"
)

const sessionKey = "appSession"

func init() { m.Register(deviceSessionSchema, nil, "1794690000_device_sessions.go") }

func deviceSessionSchema(app core.App) error {
	users, err := app.FindCollectionByNameOrId("users")
	if err != nil {
		return err
	}
	for _, name := range []string{"messages", "message_reactions", "calls"} {
		c, err := app.FindCollectionByNameOrId(name)
		if err != nil {
			return err
		}
		c.ListRule = ptr("@request.auth.id != '' && conversation.members.id ?= @request.auth.id")
		c.ViewRule = c.ListRule
		if name == "calls" {
			c.Fields.Add(&core.TextField{Name: "caller_session", Hidden: true}, &core.TextField{Name: "receiver_session", Hidden: true})
		}
		if err := app.Save(c); err != nil {
			return err
		}
	}
	users.Fields.RemoveByName("history_cutoff")
	users.Fields.RemoveByName("current_device")
	users.UpdateRule = ptr("id = @request.auth.id && @request.body.emailVisibility:changed = false && @request.body.verified:changed = false")
	if err := app.Save(users); err != nil {
		return err
	}

	codes, err := app.FindRecordsByFilter("account_codes", "purpose = 'history'", "", 0, 0)
	if err != nil {
		return err
	}
	for _, code := range codes {
		if err := app.Delete(code); err != nil {
			return err
		}
	}

	devices, err := app.FindAllRecords("devices")
	if err != nil {
		return err
	}
	for _, device := range devices {
		if err := deleteDevice(app, device); err != nil {
			return err
		}
	}
	deviceCollection, err := app.FindCollectionByNameOrId("devices")
	if err != nil {
		return err
	}
	deviceCollection.Fields.Add(&core.TextField{Name: "session", Hidden: true})
	deviceCollection.AddIndex("idx_push_session", false, "session", "")
	if err := app.Save(deviceCollection); err != nil {
		return err
	}

	activeCalls, err := app.FindRecordsByFilter("calls", "status = 'ringing' || status = 'accepted' || status = 'connected'", "", 0, 0)
	if err != nil {
		return err
	}
	for _, call := range activeCalls {
		finish(call, "ended")
		if err := app.Save(call); err != nil {
			return err
		}
	}

	owner := relation("user", users, true)
	owner.CascadeDelete = true
	sessions := collection("app_sessions", owner,
		&core.TextField{Name: "device", Required: true, Max: 128, Hidden: true},
		&core.TextField{Name: "generation", Required: true, Max: 64, Hidden: true},
		date("expires"))
	sessions.AddIndex("idx_session_device", true, "user,device", "")
	return app.Save(sessions)
}

func accountGeneration(user *core.Record) string {
	digest := sha256.Sum256([]byte(user.TokenKey()))
	return hex.EncodeToString(digest[:])
}

func requestSession(e *core.RequestEvent) *core.Record {
	session, _ := e.Get(sessionKey).(*core.Record)
	return session
}

func tokenSession(app core.App, token string, user *core.Record) *core.Record {
	claims, err := security.ParseUnverifiedJWT(token)
	if err != nil {
		return nil
	}
	id, _ := claims[sessionKey].(string)
	if id == "" {
		return nil
	}
	session, err := app.FindRecordById("app_sessions", id)
	if err != nil || session.GetString("user") != user.Id ||
		session.GetString("generation") != accountGeneration(user) ||
		!time.Now().Before(session.GetDateTime("expires").Time()) {
		return nil
	}
	return session
}

func authorizationToken(e *core.RequestEvent) string {
	token := e.Request.Header.Get("Authorization")
	if len(token) > 7 && strings.EqualFold(token[:7], "Bearer ") {
		return token[7:]
	}
	return token
}

func signSessionToken(token string, session *core.Record, key string, duration time.Duration) (string, error) {
	claims, err := security.ParseUnverifiedJWT(token)
	if err != nil {
		return "", err
	}
	claims[sessionKey] = session.Id
	return security.NewJWT(claims, key, duration)
}

func (s *service) sessionRoutes(serve *core.ServeEvent) {
	serve.Router.Bind(&hook.Handler[*core.RequestEvent]{
		Priority: apis.DefaultLoadAuthTokenMiddlewarePriority + 1,
		Func: func(e *core.RequestEvent) error {
			if e.Auth != nil && e.Auth.Collection().Name == "users" {
				session := tokenSession(e.App, authorizationToken(e), e.Auth)
				if session == nil {
					return e.UnauthorizedError("This device session has ended.", nil)
				}
				e.Set(sessionKey, session)
			}
			if strings.HasPrefix(e.Request.URL.Path, "/api/files/") {
				token := e.Request.URL.Query().Get("token")
				if token != "" {
					user, err := e.App.FindAuthRecordByToken(token, core.TokenTypeFile)
					if err == nil && user.Collection().Name == "users" && tokenSession(e.App, token, user) == nil {
						return e.NotFoundError("Record unavailable", nil)
					}
				}
			}
			return e.Next()
		},
	})

	s.app.OnRecordAuthWithPasswordRequest("users").BindFunc(func(e *core.RecordAuthWithPasswordRequestEvent) error {
		if e.Record == nil || !e.Record.ValidatePassword(e.Password) {
			return e.Next()
		}
		device := e.Request.Header.Get("X-App-Device")
		if !validDevice(device) {
			return e.BadRequestError("A valid installation ID is required.", nil)
		}
		var session *core.Record
		var rooms []*core.Record
		err := s.app.RunInTransaction(func(tx core.App) error {
			user, err := tx.FindRecordById("users", e.Record.Id)
			if err != nil {
				return err
			}
			if user.TokenKey() != e.Record.TokenKey() && !user.ValidatePassword(e.Password) {
				return errConflict
			}
			session, err = s.openSession(tx, user, device, &rooms)
			e.Record = user
			return err
		})
		if err != nil {
			return apiError(e.RequestEvent, err)
		}
		s.cleanSessionRooms(e.Request.Context(), rooms)
		e.Set(sessionKey, session)
		return e.Next()
	})

	s.app.OnRecordAuthRequest("users").BindFunc(func(e *core.RecordAuthRequestEvent) error {
		session := requestSession(e.RequestEvent)
		if session == nil {
			return e.UnauthorizedError("A device session is required.", nil)
		}
		duration := e.Record.Collection().AuthToken.DurationTime()
		var err error
		e.Token, err = signSessionToken(e.Token, session, e.Record.TokenKey()+e.Record.Collection().AuthToken.Secret, duration)
		if err != nil {
			return apiError(e.RequestEvent, err)
		}
		return e.Next()
	})

	s.app.OnRecordAuthRefreshRequest("users").BindFunc(func(e *core.RecordAuthRefreshRequestEvent) error {
		session := requestSession(e.RequestEvent)
		err := s.app.RunInTransaction(func(tx core.App) error {
			current, err := tx.FindRecordById("app_sessions", session.Id)
			if err != nil {
				return err
			}
			current.Set("expires", time.Now().Add(e.Record.Collection().AuthToken.DurationTime()))
			return tx.Save(current)
		})
		if err != nil {
			return e.UnauthorizedError("This device session has ended.", err)
		}
		return e.Next()
	})

	s.app.OnFileTokenRequest("users").BindFunc(func(e *core.FileTokenRequestEvent) error {
		session := requestSession(e.RequestEvent)
		if session == nil {
			return e.UnauthorizedError("A device session is required.", nil)
		}
		var err error
		e.Token, err = signSessionToken(e.Token, session, e.Record.TokenKey()+e.Record.Collection().FileToken.Secret, e.Record.Collection().FileToken.DurationTime())
		if err != nil {
			return apiError(e.RequestEvent, err)
		}
		return e.Next()
	})

	s.app.OnRealtimeSubscribeRequest().BindFunc(func(e *core.RealtimeSubscribeRequestEvent) error {
		e.Client.Unset(sessionKey)
		if requestSession(e.RequestEvent) != nil {
			e.Client.Set(sessionKey, authorizationToken(e.RequestEvent))
		}
		return e.Next()
	})
	s.app.OnRealtimeMessageSend().BindFunc(func(e *core.RealtimeMessageEvent) error {
		token, _ := e.Client.Get(sessionKey).(string)
		if token != "" {
			user, err := e.App.FindAuthRecordByToken(token, core.TokenTypeAuth)
			if err != nil || tokenSession(e.App, token, user) == nil {
				return nil
			}
		}
		return e.Next()
	})
}

func (s *service) openSession(app core.App, user *core.Record, device string, rooms *[]*core.Record) (*core.Record, error) {
	var rows []*core.Record
	err := app.RecordQuery("app_sessions").Where(dbx.HashExp{"user": user.Id}).OrderBy("rowid").All(&rows)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	generation := accountGeneration(user)
	active := make([]*core.Record, 0, len(rows))
	for _, row := range rows {
		if row.GetString("device") == device || row.GetString("generation") != generation || !now.Before(row.GetDateTime("expires").Time()) {
			if err := s.revokeSession(app, row, rooms); err != nil {
				return nil, err
			}
		} else {
			active = append(active, row)
		}
	}
	for i := 0; i < len(active)-1; i++ {
		if err := s.revokeSession(app, active[i], rooms); err != nil {
			return nil, err
		}
	}
	session, err := newRow(app, "app_sessions")
	if err != nil {
		return nil, err
	}
	session.Set("user", user.Id)
	session.Set("device", device)
	session.Set("generation", generation)
	session.Set("expires", now.Add(user.Collection().AuthToken.DurationTime()))
	return session, app.Save(session)
}

func (s *service) revokeSession(app core.App, session *core.Record, rooms *[]*core.Record) error {
	devices, err := app.FindRecordsByFilter("devices", "session = {:id}", "", 0, 0, dbx.Params{"id": session.Id})
	if err != nil {
		return err
	}
	for _, device := range devices {
		if err := deleteDevice(app, device); err != nil {
			return err
		}
	}
	calls, err := app.FindRecordsByFilter("calls", "(caller_session = {:id} || receiver_session = {:id}) && (status = 'ringing' || status = 'accepted' || status = 'connected')", "", 0, 0, dbx.Params{"id": session.Id})
	if err != nil {
		return err
	}
	for _, call := range calls {
		finish(call, "ended")
		if err := app.Save(call); err != nil {
			return err
		}
		if rooms != nil && call.GetBool("cleanup_pending") {
			*rooms = append(*rooms, call)
		}
	}
	return app.Delete(session)
}

func (s *service) cleanSessionRooms(ctx context.Context, rooms []*core.Record) {
	for _, room := range rooms {
		s.deleteRoom(ctx, room)
	}
}

func (s *service) logoutSession(e *core.RequestEvent) error {
	var rooms []*core.Record
	err := s.app.RunInTransaction(func(tx core.App) error { return s.revokeSession(tx, requestSession(e), &rooms) })
	if err != nil {
		return apiError(e, err)
	}
	s.cleanSessionRooms(e.Request.Context(), rooms)
	return e.NoContent(204)
}
