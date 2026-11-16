package main

import (
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
	"github.com/pocketbase/pocketbase/tools/filesystem"
)

const attachmentMax = 50 << 20
const mediaQuota = 2 << 30

func init() { m.Register(messageDeletionSchema, nil, "1794780000_message_deletion.go") }

func init() { m.Register(deliverySchema, nil, "1794850000_delivery.go") }

func deliverySchema(app core.App) error {
	members, err := app.FindCollectionByNameOrId("conversation_members")
	if err != nil {
		return err
	}
	members.Fields.Add(date("delivered_through"))
	members.Fields.Add(&core.TextField{Name: "delivered_id", Max: 15})
	return app.Save(members)
}

func messageDeletionSchema(app core.App) error {
	messages, err := app.FindCollectionByNameOrId("messages")
	if err != nil {
		return err
	}
	messages.Fields.Add(date("deleted_at"))
	if err := app.Save(messages); err != nil {
		return err
	}
	users, err := app.FindCollectionByNameOrId("users")
	if err != nil {
		return err
	}
	conversations, err := app.FindCollectionByNameOrId("conversations")
	if err != nil {
		return err
	}
	user := relation("user", users, true)
	message := relation("message", messages, true)
	conversation := relation("conversation", conversations, true)
	user.CascadeDelete, message.CascadeDelete, conversation.CascadeDelete = true, true, true
	hides := collection("message_hides", user, message, conversation)
	hides.ListRule = ptr("@request.auth.id != '' && user = @request.auth.id && conversation.members.id ?= @request.auth.id")
	hides.ViewRule = hides.ListRule
	hides.AddIndex("idx_message_hide_user", true, "user,message", "")
	hides.AddIndex("idx_message_hide_message", false, "message", "")
	if err := app.Save(hides); err != nil {
		return err
	}
	const memberRule = "@request.auth.id != '' && conversation.members.id ?= @request.auth.id"
	const hideRule = "message_hides_via_message.user != @request.auth.id"
	messages.ListRule = ptr(memberRule + " && " + hideRule)
	messages.ViewRule = messages.ListRule
	if err := app.Save(messages); err != nil {
		return err
	}
	reactions, err := app.FindCollectionByNameOrId("message_reactions")
	if err != nil {
		return err
	}
	reactions.ListRule = ptr(memberRule + " && message.deleted_at = '' && message." + hideRule)
	reactions.ViewRule = reactions.ListRule
	return app.Save(reactions)
}

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
			if err := checkMessageHide(tx, msg.Id, e.Auth.Id); err != nil {
				return err
			}
			status = 200
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if body.ReplyTo != "" {
			reply, err := messageInConversation(tx, body.ReplyTo, e.Auth.Id)
			if err != nil || reply.GetString("conversation") != conv.Id || !reply.GetDateTime("deleted_at").IsZero() {
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
	msg, err := memberMessage(app, id, user)
	if err != nil {
		return nil, err
	}
	return msg, checkMessageHide(app, id, user)
}

func memberMessage(app core.App, id, user string) (*core.Record, error) {
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
	return msg, nil
}

func checkMessageHide(app core.App, message, user string) error {
	_, err := app.FindFirstRecordByFilter("message_hides", "user = {:u} && message = {:m}", dbx.Params{"u": user, "m": message})
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err == nil {
		return errMissing
	}
	return err
}

func (s *service) deleteMessage(e *core.RequestEvent) error {
	scope := e.Request.URL.Query().Get("scope")
	if scope != "me" && scope != "everyone" {
		return apiError(e, errInvalid)
	}
	var msg *core.Record
	err := s.app.RunInTransaction(func(tx core.App) error {
		var err error
		msg, err = memberMessage(tx, e.Request.PathValue("id"), e.Auth.Id)
		if err != nil {
			return err
		}
		if scope == "me" {
			if err := checkMessageHide(tx, msg.Id, e.Auth.Id); err != nil {
				if errors.Is(err, errMissing) {
					return nil
				}
				return err
			}
			hide, err := newRow(tx, "message_hides")
			if err != nil {
				return err
			}
			hide.Set("user", e.Auth.Id)
			hide.Set("message", msg.Id)
			hide.Set("conversation", msg.GetString("conversation"))
			return tx.Save(hide)
		}
		if msg.GetString("sender") != e.Auth.Id {
			return errMissing
		}
		if !msg.GetDateTime("deleted_at").IsZero() {
			return nil
		}
		reactions, err := tx.FindRecordsByFilter("message_reactions", "message = {:m}", "", 0, 0, dbx.Params{"m": msg.Id})
		if err != nil {
			return err
		}
		for _, reaction := range reactions {
			if err := tx.Delete(reaction); err != nil {
				return err
			}
		}
		msg.Set("text", "This message was deleted.")
		msg.Set("kind", "text")
		msg.Set("attachment", "")
		msg.Set("attachment_bytes", 0)
		msg.Set("reply_to", "")
		msg.Set("deleted_at", time.Now())
		return tx.Save(msg)
	})
	if err != nil {
		return apiError(e, err)
	}
	if scope == "me" {
		return e.NoContent(204)
	}
	return e.JSON(200, msg)
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
		if msg.GetString("sender") != e.Auth.Id || !msg.GetDateTime("deleted_at").IsZero() {
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
		if !msg.GetDateTime("deleted_at").IsZero() {
			return errMissing
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
	return s.advanceCursor(e, "read_through", "read_id")
}

func (s *service) deliveredCursor(e *core.RequestEvent) error {
	return s.advanceCursor(e, "delivered_through", "delivered_id")
}

func (s *service) advanceCursor(e *core.RequestEvent, timeField, idField string) error {
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
		prev := member.GetDateTime(timeField)
		if when.Time().Before(prev.Time()) || (when.Time().Equal(prev.Time()) && msg.Id <= member.GetString(idField)) {
			return nil
		}
		member.Set(timeField, when)
		member.Set(idField, msg.Id)
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
