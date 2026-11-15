package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"errors"
	"html"
	"net/mail"
	"os"
	"strings"
	"time"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
	"github.com/pocketbase/pocketbase/tools/mailer"
	"github.com/pocketbase/pocketbase/tools/security"
)

const brand = "Apun Ka Communication"
const webOrigin = "https://chat.tmadhu.me"

func init() { m.Register(accountSchema, nil, "1794679200_accounts.go") }

func init() { m.Register(profileSchema, nil, "1794682800_profiles.go") }

func profileSchema(app core.App) error {
	users, err := app.FindCollectionByNameOrId("users")
	if err != nil {
		return err
	}
	avatar := users.Fields.GetByName("avatar").(*core.FileField)
	avatar.MaxSize = 3 << 20
	avatar.MimeTypes = []string{"image/jpeg", "image/png", "image/webp", "image/gif"}
	avatar.Thumbs = []string{"96x96", "192x192"}
	avatar.Protected = true
	users.ViewRule = ptr("id = @request.auth.id || (@request.context = 'protectedFile' && @request.auth.id != '' && conversations_via_members.members.id ?= @request.auth.id)")
	return app.Save(users)
}

func accountSchema(app core.App) error {
	users, err := app.FindCollectionByNameOrId("users")
	if err != nil {
		return err
	}
	users.Fields.Add(&core.TextField{Name: "current_device", Max: 128, Hidden: true}, date("history_cutoff"))
	users.AuthAlert.Enabled = false
	users.OTP.Enabled = false
	users.UpdateRule = ptr("id = @request.auth.id && @request.body.emailVisibility:changed = false && @request.body.verified:changed = false && @request.body.current_device:isset = false && @request.body.history_cutoff:isset = false")
	users.ResetPasswordTemplate.Subject = "Reset your Apun Ka Communication password"
	users.ResetPasswordTemplate.Body = mailLayout("Reset your password", "Use the button to choose a new password.", "Reset password", webOrigin+"/#reset/{TOKEN}", "")
	users.ConfirmEmailChangeTemplate.Subject = "Confirm your new email for Apun Ka Communication"
	users.ConfirmEmailChangeTemplate.Body = mailLayout("Confirm your new email", "Open the app and enter your current password to confirm this address.", "Confirm email", webOrigin+"/#email/{TOKEN}", "")
	if err := app.Save(users); err != nil {
		return err
	}
	for _, name := range []string{"messages", "message_reactions", "calls"} {
		c, err := app.FindCollectionByNameOrId(name)
		if err != nil {
			return err
		}
		field := "created"
		if name == "message_reactions" {
			field = "message.created"
		}
		rule := "@request.auth.id != '' && conversation.members.id ?= @request.auth.id && (@request.auth.history_cutoff = '' || " + field + " > @request.auth.history_cutoff)"
		c.ListRule, c.ViewRule = ptr(rule), ptr(rule)
		if err := app.Save(c); err != nil {
			return err
		}
	}
	c := collection("account_codes", relation("user", users, true), &core.TextField{Name: "purpose", Required: true, Max: 20},
		&core.TextField{Name: "digest", Required: true, Max: 64, Hidden: true}, &core.TextField{Name: "session", Max: 64, Hidden: true},
		&core.NumberField{Name: "attempts", OnlyInt: true}, date("expires"))
	c.AddIndex("idx_account_code", true, "user,purpose", "")
	return app.Save(c)
}

func (s *service) accountRoutes(e *core.ServeEvent) {
	s.sessionRoutes(e)
	auth := e.Router.Group("/api/app/account").Bind(apis.RequireAuth("users"))
	auth.GET("/session", func(e *core.RequestEvent) error {
		return e.JSON(200, map[string]bool{"active": true})
	})
	auth.POST("/password", s.changePassword)
	auth.POST("/logout", s.logoutSession)
	e.Router.POST("/api/app/account/register", s.registerAccount)
	e.Router.POST("/api/app/account/recovery/request", s.requestRecoveryCode)
	e.Router.POST("/api/app/account/recovery/confirm", s.confirmRecoveryCode)
}

type newPassword struct {
	Password        string `json:"password"`
	PasswordConfirm string `json:"passwordConfirm"`
}

func (p newPassword) valid() bool {
	return len(p.Password) >= 8 && len(p.Password) <= 72 && p.Password == p.PasswordConfirm
}

func validDevice(device string) bool {
	return len(device) >= 24 && len(device) <= 128
}

func (s *service) registerAccount(e *core.RequestEvent) error {
	var body struct {
		newPassword
		Email string `json:"email"`
		Name  string `json:"name"`
	}
	device := e.Request.Header.Get("X-App-Device")
	if e.BindBody(&body) != nil || !validDevice(device) || !body.valid() {
		return e.BadRequestError("Enter a matching password of 8 to 72 bytes and try again.", nil)
	}
	email := strings.ToLower(strings.TrimSpace(body.Email))
	name := strings.TrimSpace(body.Name)
	address, err := mail.ParseAddress(email)
	if name == "" || len([]rune(name)) > 80 || err != nil || address.Address != email {
		return e.BadRequestError("Enter a display name and a valid email address.", nil)
	}
	user, err := newRow(s.app, "users")
	if err != nil {
		return apiError(e, err)
	}
	user.SetEmail(email)
	user.SetPassword(body.Password)
	user.Set("name", name)
	var session *core.Record
	if err := s.app.RunInTransaction(func(tx core.App) error {
		if err := tx.Save(user); err != nil {
			return err
		}
		var err error
		session, err = s.openSession(tx, user, device, nil)
		return err
	}); err != nil {
		if _, lookupErr := s.app.FindAuthRecordByEmail("users", email); lookupErr == nil {
			return e.BadRequestError("This email already has an account. Sign in or reset your password.", nil)
		}
		return e.BadRequestError("Could not create an account with this email address.", err)
	}
	e.Set(sessionKey, session)
	return apis.RecordAuthResponse(e, user, "", nil)
}

func (s *service) changePassword(e *core.RequestEvent) error {
	var body newPassword
	if e.BindBody(&body) != nil || !body.valid() {
		return e.BadRequestError("Enter a matching password of 8 to 72 bytes.", nil)
	}
	session := requestSession(e)
	var replacement, user *core.Record
	var rooms []*core.Record
	err := s.app.RunInTransaction(func(tx core.App) error {
		if _, err := tx.FindRecordById("app_sessions", session.Id); err != nil {
			return errConflict
		}
		var err error
		user, err = tx.FindRecordById("users", e.Auth.Id)
		if err != nil {
			return err
		}
		if user.TokenKey() != e.Auth.TokenKey() {
			return errConflict
		}
		user.SetPassword(body.Password)
		user.SetTokenKey(security.RandomString(50))
		if err := tx.Save(user); err != nil {
			return err
		}
		replacement, err = s.openSession(tx, user, session.GetString("device"), &rooms)
		return err
	})
	if err != nil {
		return apiError(e, err)
	}
	s.cleanSessionRooms(e.Request.Context(), rooms)
	e.Set(sessionKey, replacement)
	return apis.RecordAuthResponse(e, user, "", nil)
}

func (s *service) codeDigest(id, purpose, code string) string {
	mac := hmac.New(sha256.New, []byte(s.secret))
	mac.Write([]byte(id + ":" + purpose + ":" + code))
	return hex.EncodeToString(mac.Sum(nil))
}

func (s *service) requestRecoveryCode(e *core.RequestEvent) error {
	const purpose = "recovery"
	if s.secret == "" || !s.app.Settings().SMTP.Enabled {
		return e.JSON(503, map[string]string{"message": "Email is unavailable."})
	}
	var body struct{ Email string }
	if e.BindBody(&body) != nil {
		return apiError(e, errInvalid)
	}
	user, err := s.app.FindAuthRecordByEmail("users", strings.TrimSpace(body.Email))
	if errors.Is(err, sql.ErrNoRows) {
		return e.NoContent(204)
	}
	if err != nil {
		return apiError(e, err)
	}
	code := security.RandomStringWithAlphabet(6, "0123456789")
	err = s.app.RunInTransaction(func(tx core.App) error {
		row, err := tx.FindFirstRecordByFilter("account_codes", "user = {:u} && purpose = {:p}", dbx.Params{"u": user.Id, "p": purpose})
		if errors.Is(err, sql.ErrNoRows) {
			row, err = newRow(tx, "account_codes")
		}
		if err != nil {
			return err
		}
		if !row.GetDateTime("expires").IsZero() && time.Until(row.GetDateTime("expires").Time()) > 9*time.Minute {
			return errConflict
		}
		row.Set("user", user.Id)
		row.Set("purpose", purpose)
		row.Set("expires", time.Now().Add(10*time.Minute))
		row.Set("attempts", 0)
		row.Set("digest", s.codeDigest(user.Id, purpose, code))
		row.Set("session", s.codeDigest(user.Id, purpose, user.TokenKey()))
		return tx.Save(row)
	})
	if errors.Is(err, errConflict) {
		return e.NoContent(204)
	}
	if err != nil {
		return apiError(e, err)
	}
	title, message := "Your password recovery code", "Enter this code in Apun Ka Communication to set a new password."
	settings := s.app.Settings()
	msg := &mailer.Message{From: mail.Address{Name: brand, Address: settings.Meta.SenderAddress}, To: []mail.Address{{Address: user.Email()}}, Subject: title + " · " + brand, Text: message + " Code: " + code + ". It expires in 10 minutes.", HTML: mailLayout(title, message, "", "", code)}
	if err := s.app.NewMailClient().Send(msg); err != nil {
		s.app.Logger().Error("Account code email failed", "purpose", purpose)
	}
	return e.NoContent(204)
}

func (s *service) confirmRecoveryCode(e *core.RequestEvent) error {
	const purpose = "recovery"
	var body struct{ Email, Code, Password, PasswordConfirm string }
	if e.BindBody(&body) != nil || len(body.Code) != 6 {
		return apiError(e, errInvalid)
	}
	if !(newPassword{body.Password, body.PasswordConfirm}).valid() {
		return apiError(e, errInvalid)
	}
	var valid bool
	err := s.app.RunInTransaction(func(tx core.App) error {
		user, err := tx.FindAuthRecordByEmail("users", strings.TrimSpace(body.Email))
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		row, err := tx.FindFirstRecordByFilter("account_codes", "user = {:u} && purpose = {:p}", dbx.Params{"u": user.Id, "p": purpose})
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if row.GetInt("attempts") >= 5 || !time.Now().Before(row.GetDateTime("expires").Time()) || row.GetString("session") != s.codeDigest(user.Id, purpose, user.TokenKey()) {
			return nil
		}
		if subtle.ConstantTimeCompare([]byte(row.GetString("digest")), []byte(s.codeDigest(user.Id, purpose, body.Code))) != 1 {
			row.Set("attempts", row.GetInt("attempts")+1)
			return tx.Save(row)
		}
		user.SetPassword(body.Password)
		user.SetTokenKey(security.RandomString(50))
		if err := tx.Save(user); err != nil {
			return err
		}
		if err := tx.Delete(row); err != nil {
			return err
		}
		valid = true
		return nil
	})
	if err != nil {
		return apiError(e, err)
	}
	if !valid {
		return e.BadRequestError("Invalid or expired code.", nil)
	}
	return e.NoContent(204)
}

func mailLayout(title, body, action, link, code string) string {
	content := ""
	if link != "" {
		content = `<a href="` + html.EscapeString(link) + `" style="display:inline-block;background:#176951;color:#fff;padding:13px 21px;border-radius:9px;font-weight:bold;text-decoration:none">` + html.EscapeString(action) + `</a>`
	}
	if code != "" {
		content = `<div style="background:#eef5f1;border-radius:12px;padding:16px;font-size:30px;letter-spacing:7px;font-weight:bold">` + html.EscapeString(code) + `</div><p style="font-size:13px">This code expires in 10 minutes.</p>`
	}
	return `<table role="presentation" width="100%" style="background:#eff4f2;color:#172c25;font-family:Arial,sans-serif"><tr><td align="center" style="padding:28px 12px"><table role="presentation" width="100%" style="max-width:520px;background:#fff;border-radius:18px"><tr><td style="padding:28px"><div style="color:#176951;font-size:13px;font-weight:bold">Apun Ka Communication</div><h1 style="font-size:27px;margin:14px 0">` + html.EscapeString(title) + `</h1><p style="line-height:1.55">` + html.EscapeString(body) + `</p>` + content + `<p style="border-top:1px solid #e2e9e6;padding-top:16px;margin-top:24px;color:#50645d;font-size:13px">If you didn’t request this, you can ignore this email.</p></td></tr></table></td></tr></table>`
}

func (s *service) mailConfig(settings *core.Settings) {
	settings.Meta.AppName = brand
	settings.Meta.SenderName = brand
	if login, password := os.Getenv("SMTP_USERNAME"), os.Getenv("SMTP_PASSWORD"); login != "" && password != "" {
		settings.Meta.SenderAddress = login
		settings.SMTP.Enabled = true
		settings.SMTP.Host = "smtp.gmail.com"
		settings.SMTP.Port = 465
		settings.SMTP.TLS = true
		settings.SMTP.Username = login
		settings.SMTP.Password = password
	}
}
