package main

import (
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/filesystem"
)

const attachmentMax = 50 << 20
const mediaQuota = 2 << 30

func (s *service) people(e *core.RequestEvent) error {
	conv, err := memberRecord(s.app, e.Request.PathValue("id"), e.Auth.Id)
	if err != nil {
		return apiError(e, err)
	}
	people := make([]map[string]string, 0, 2)
	for _, id := range conv.GetStringSlice("members") {
		user, err := s.app.FindRecordById("users", id)
		if err != nil {
			return apiError(e, err)
		}
		people = append(people, map[string]string{"id": id, "name": user.GetString("name"), "avatar": user.GetString("avatar")})
	}
	return e.JSON(200, people)
}

func (s *service) sendMessage(e *core.RequestEvent) error {
	var body struct {
		Conversation string `json:"conversation" form:"conversation"`
		Text         string `json:"text" form:"text"`
		ClientID     string `json:"client_message_id" form:"client_message_id"`
		Kind         string `json:"kind" form:"kind"`
		ReplyTo      string `json:"reply_to" form:"reply_to"`
		InCall       bool   `json:"in_call" form:"in_call"`
	}
	if e.BindBody(&body) != nil || len(body.ClientID) == 0 || len(body.ClientID) > 64 || len(body.Text) > 10000 {
		return apiError(e, errInvalid)
	}
	var attachment *filesystem.File
	if body.Kind != "text" {
		files, err := e.FindUploadedFiles("attachment")
		if err != nil || len(files) != 1 || files[0].Size > attachmentMax || files[0].Size <= 0 {
			return apiError(e, errInvalid)
		}
		attachment = files[0]
	} else if strings.TrimSpace(body.Text) == "" {
		return apiError(e, errInvalid)
	}
	switch body.Kind {
	case "text", "image", "voice", "video":
	default:
		return apiError(e, errInvalid)
	}
	var msg *core.Record
	status := 201
	err := s.app.RunInTransaction(func(tx core.App) error {
		conv, err := memberRecord(tx, body.Conversation, e.Auth.Id)
		if err != nil {
			return err
		}
		msg, err = tx.FindFirstRecordByFilter("messages", "sender = {:s} && client_message_id = {:id}", dbx.Params{"s": e.Auth.Id, "id": body.ClientID})
		if err == nil {
			if msg.GetString("conversation") != conv.Id {
				return errConflict
			}
			if !historyAllows(e.Auth, msg) {
				return errMissing
			}
			status = 200
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if body.ReplyTo != "" {
			reply, err := tx.FindRecordById("messages", body.ReplyTo)
			if err != nil || reply.GetString("conversation") != conv.Id || !historyAllows(e.Auth, reply) {
				return errInvalid
			}
		}
		msg, err = newRow(tx, "messages")
		if err != nil {
			return err
		}
		msg.Set("conversation", conv.Id)
		msg.Set("sender", e.Auth.Id)
		msg.Set("client_message_id", body.ClientID)
		msg.Set("kind", body.Kind)
		msg.Set("text", body.Text)
		msg.Set("reply_to", body.ReplyTo)
		msg.Set("in_call", body.InCall)
		if attachment != nil {
			var used int64
			if err := tx.DB().NewQuery("SELECT coalesce(sum(attachment_bytes),0) FROM messages").Row(&used); err != nil {
				return err
			}
			if used+attachment.Size > mediaQuota || !diskAllowsUpload(tx.DataDir()) {
				return errQuota
			}
			msg.Set("attachment", attachment)
			msg.Set("attachment_bytes", attachment.Size)
		}
		if err := tx.Save(msg); err != nil {
			return err
		}
		for _, recipient := range conv.GetStringSlice("members") {
			if recipient != e.Auth.Id {
				return enqueue(tx, recipient, "new_message", conv.Id, time.Now().Add(15*time.Minute))
			}
		}
		return nil
	})
	if err != nil {
		return apiError(e, err)
	}
	return e.JSON(status, msg)
}

func messageInConversation(app core.App, id, user string) (*core.Record, error) {
	msg, err := app.FindRecordById("messages", id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, errMissing
	}
	if err != nil {
		return nil, err
	}
	if _, err = memberRecord(app, msg.GetString("conversation"), user); err != nil {
		return nil, err
	}
	who, err := app.FindRecordById("users", user)
	if err != nil {
		return nil, err
	}
	if !historyAllows(who, msg) {
		return nil, errMissing
	}
	return msg, nil
}

func historyAllows(user, record *core.Record) bool {
	cutoff := user.GetDateTime("history_cutoff")
	return cutoff.IsZero() || record.GetDateTime("created").Time().After(cutoff.Time())
}

func (s *service) editMessage(e *core.RequestEvent) error {
	var body struct {
		Text string `json:"text"`
	}
	if e.BindBody(&body) != nil || strings.TrimSpace(body.Text) == "" || len(body.Text) > 10000 {
		return apiError(e, errInvalid)
	}
	var msg *core.Record
	err := s.app.RunInTransaction(func(tx core.App) error {
		var err error
		msg, err = messageInConversation(tx, e.Request.PathValue("id"), e.Auth.Id)
		if err != nil {
			return err
		}
		if msg.GetString("sender") != e.Auth.Id {
			return errMissing
		}
		msg.Set("text", body.Text)
		return tx.Save(msg)
	})
	if err != nil {
		return apiError(e, err)
	}
	return e.JSON(200, msg)
}

func (s *service) react(e *core.RequestEvent) error {
	var body struct {
		Emoji string `json:"emoji"`
	}
	if e.BindBody(&body) != nil || len([]rune(body.Emoji)) > 24 {
		return apiError(e, errInvalid)
	}
	err := s.app.RunInTransaction(func(tx core.App) error {
		msg, err := messageInConversation(tx, e.Request.PathValue("id"), e.Auth.Id)
		if err != nil {
			return err
		}
		reaction, err := tx.FindFirstRecordByFilter("message_reactions", "message = {:m} && user = {:u}", dbx.Params{"m": msg.Id, "u": e.Auth.Id})
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if body.Emoji == "" {
			if reaction == nil {
				return nil
			}
			return tx.Delete(reaction)
		}
		if reaction == nil {
			reaction, err = newRow(tx, "message_reactions")
			if err != nil {
				return err
			}
			reaction.Set("message", msg.Id)
			reaction.Set("user", e.Auth.Id)
			reaction.Set("conversation", msg.GetString("conversation"))
		}
		reaction.Set("emoji", body.Emoji)
		return tx.Save(reaction)
	})
	if err != nil {
		return apiError(e, err)
	}
	return e.NoContent(204)
}

func (s *service) readCursor(e *core.RequestEvent) error {
	var body struct {
		Message string `json:"message"`
	}
	if e.BindBody(&body) != nil || body.Message == "" {
		return apiError(e, errInvalid)
	}
	conv := e.Request.PathValue("id")
	err := s.app.RunInTransaction(func(tx core.App) error {
		msg, err := messageInConversation(tx, body.Message, e.Auth.Id)
		if err != nil {
			return err
		}
		if msg.GetString("conversation") != conv {
			return errMissing
		}
		member, err := findMembership(tx, conv, e.Auth.Id)
		if err != nil {
			return err
		}
		when := msg.GetDateTime("created")
		prev := member.GetDateTime("read_through")
		if when.Time().Before(prev.Time()) || (when.Time().Equal(prev.Time()) && msg.Id <= member.GetString("read_id")) {
			return nil
		}
		member.Set("read_through", when)
		member.Set("read_id", msg.Id)
		return tx.Save(member)
	})
	if err != nil {
		return apiError(e, err)
	}
	return e.NoContent(204)
}

func (s *service) presence(e *core.RequestEvent) error {
	var body struct {
		Typing bool `json:"typing"`
	}
	if e.BindBody(&body) != nil {
		return apiError(e, errInvalid)
	}
	conv := e.Request.PathValue("id")
	err := s.app.RunInTransaction(func(tx core.App) error {
		if _, err := memberRecord(tx, conv, e.Auth.Id); err != nil {
			return err
		}
		member, err := findMembership(tx, conv, e.Auth.Id)
		if err != nil {
			return err
		}
		now := time.Now()
		member.Set("presence_until", now.Add(45*time.Second))
		if body.Typing {
			member.Set("typing_until", now.Add(8*time.Second))
		} else {
			member.Set("typing_until", "")
		}
		return tx.Save(member)
	})
	if err != nil {
		return apiError(e, err)
	}
	return e.NoContent(204)
}
