package main

import (
	"database/sql"
	"errors"
	"slices"
	"strings"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

func init() {
	m.Register(schema, nil, "1794678000_initial.go")
	m.Register(memberAPI, nil, "1794678100_member_api.go")
}

func memberAPI(app core.App) error {
	for _, name := range []string{"conversations", "conversation_members", "messages", "message_reactions", "calls"} {
		c, err := app.FindCollectionByNameOrId(name)
		if err != nil {
			return err
		}
		rule := "@request.auth.id != '' && conversation.members.id ?= @request.auth.id"
		if name == "conversations" {
			rule = "@request.auth.id != '' && members.id ?= @request.auth.id"
		}
		c.ListRule, c.ViewRule = ptr(rule), ptr(rule)
		if err := app.Save(c); err != nil {
			return err
		}
	}
	return nil
}

func schema(app core.App) error {
	users, err := app.FindCollectionByNameOrId("users")
	if err != nil {
		return err
	}
	users.ListRule = ptr("id = @request.auth.id")
	users.ViewRule = users.ListRule
	users.UpdateRule = ptr("id = @request.auth.id && @request.body.emailVisibility:changed = false && @request.body.verified:changed = false")
	users.CreateRule = nil
	users.DeleteRule = nil
	if f, ok := users.Fields.GetByName("avatar").(*core.FileField); ok {
		f.Protected = true
	}
	if err := app.Save(users); err != nil {
		return err
	}

	conversations := collection("conversations",
		&core.RelationField{Name: "members", CollectionId: users.Id, Required: true, MinSelect: 2, MaxSelect: 2},
		&core.TextField{Name: "pair", Required: true, Max: 32, Hidden: true})
	conversations.ListRule = ptr("@request.auth.id != '' && members ?= @request.auth.id")
	conversations.ViewRule = conversations.ListRule
	conversations.AddIndex("idx_conversation_pair", true, "pair", "")
	if err := app.Save(conversations); err != nil {
		return err
	}

	memberRule := "@request.auth.id != '' && conversation.members ?= @request.auth.id"
	membership := collection("conversation_members", relation("conversation", conversations, true), relation("user", users, true),
		date("read_through"), &core.TextField{Name: "read_id", Max: 15}, date("presence_until"), date("typing_until"))
	membership.ListRule, membership.ViewRule = ptr(memberRule), ptr(memberRule)
	membership.AddIndex("idx_conversation_member", true, "conversation,user", "")
	if err := app.Save(membership); err != nil {
		return err
	}

	messages := collection("messages", relation("conversation", conversations, true), relation("sender", users, true),
		&core.TextField{Name: "text", Max: 10000}, &core.TextField{Name: "client_message_id", Max: 64, Required: true},
		&core.SelectField{Name: "kind", Required: true, Values: []string{"text", "image", "voice", "video"}},
		&core.FileField{Name: "attachment", MaxSize: 50 * 1024 * 1024, MaxSelect: 1, Protected: true,
			MimeTypes: []string{"image/jpeg", "image/png", "image/webp", "audio/webm", "audio/ogg", "audio/mpeg", "audio/mp4", "audio/aac", "video/mp4", "video/webm", "application/ogg"},
			Thumbs:    []string{"400x0"}},
		&core.NumberField{Name: "attachment_bytes", OnlyInt: true},
		&core.BoolField{Name: "in_call"})
	messages.ListRule, messages.ViewRule = ptr(memberRule), ptr(memberRule)
	messages.AddIndex("idx_message_dedup", true, "sender,client_message_id", "")
	messages.AddIndex("idx_message_order", false, "conversation,created,id", "")
	if err := app.Save(messages); err != nil {
		return err
	}
	messages.Fields.Add(relation("reply_to", messages, false))
	if err := app.Save(messages); err != nil {
		return err
	}

	reactions := collection("message_reactions", relation("conversation", conversations, true), relation("message", messages, true),
		relation("user", users, true), &core.TextField{Name: "emoji", Required: true, Max: 24})
	reactions.ListRule, reactions.ViewRule = ptr(memberRule), ptr(memberRule)
	reactions.AddIndex("idx_message_reaction", true, "message,user", "")
	if err := app.Save(reactions); err != nil {
		return err
	}

	calls := collection("calls", relation("conversation", conversations, true), relation("caller", users, true), relation("receiver", users, true),
		&core.TextField{Name: "room", Required: true, Max: 80}, &core.SelectField{Name: "kind", Required: true, Values: []string{"audio", "video"}},
		&core.SelectField{Name: "status", Required: true, Values: []string{"ringing", "accepted", "connected", "ended", "rejected", "cancelled", "missed", "failed"}},
		date("ring_expires_at"), date("accepted_at"), date("connected_at"), date("ended_at"), date("empty_since"),
		&core.TextField{Name: "accepted_device", Max: 80, Hidden: true}, &core.BoolField{Name: "cleanup_pending", Hidden: true})
	calls.ListRule, calls.ViewRule = ptr(memberRule), ptr(memberRule)
	calls.AddIndex("idx_call_room", true, "room", "")
	calls.AddIndex("idx_call_status", false, "status", "")
	if err := app.Save(calls); err != nil {
		return err
	}

	devices := collection("devices", relation("user", users, true),
		&core.SelectField{Name: "platform", Required: true, Values: []string{"android", "web"}},
		&core.TextField{Name: "destination", Required: true, Max: 4096, Hidden: true},
		&core.TextField{Name: "p256dh", Max: 150, Hidden: true}, &core.TextField{Name: "auth_secret", Max: 100, Hidden: true})
	devices.ListRule = ptr("@request.auth.id != '' && user = @request.auth.id")
	devices.ViewRule = devices.ListRule
	devices.AddIndex("idx_push_destination", true, "platform,destination", "")
	if err := app.Save(devices); err != nil {
		return err
	}

	outbox := collection("notification_outbox", relation("user", users, true),
		&core.TextField{Name: "event", Required: true, Max: 24}, &core.TextField{Name: "reference", Required: true, Max: 80},
		relation("device", devices, true), date("expires_at"), date("next_attempt"),
		&core.NumberField{Name: "attempts", OnlyInt: true})
	outbox.AddIndex("idx_push_due", false, "next_attempt", "")
	return app.Save(outbox)
}

func collection(name string, fields ...core.Field) *core.Collection {
	c := core.NewBaseCollection(name)
	c.Fields.Add(fields...)
	c.Fields.Add(&core.AutodateField{Name: "created", OnCreate: true}, &core.AutodateField{Name: "updated", OnCreate: true, OnUpdate: true})
	return c
}

func relation(name string, col *core.Collection, required bool) *core.RelationField {
	return &core.RelationField{Name: name, CollectionId: col.Id, Required: required, MaxSelect: 1}
}
func date(name string) *core.DateField { return &core.DateField{Name: name} }

func pairKey(members []string) string {
	members = slices.Clone(members)
	slices.Sort(members)
	return strings.Join(members, ":")
}

func findConversation(app core.App, members []string) (*core.Record, error) {
	pair := pairKey(members)
	existing, err := app.FindFirstRecordByData("conversations", "pair", pair)
	if err == nil && pairKey(existing.GetStringSlice("members")) == pair {
		return existing, nil
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return existing, err
	}
	// Dashboard-created chats can have a manually entered pair key.
	var manual core.Record
	err = app.RecordQuery("conversations").
		Where(dbx.NewExp(`(SELECT count(DISTINCT value) FROM json_each(conversations.members) WHERE value IN ({:a},{:b})) = 2`,
			dbx.Params{"a": members[0], "b": members[1]})).
		OrderBy("created", "id").Limit(1).One(&manual)
	return &manual, err
}

func ensureConversation(app core.App, members []string) (*core.Record, error) {
	r, err := findConversation(app, members)
	if errors.Is(err, sql.ErrNoRows) {
		r, err = newRow(app, "conversations")
		if err != nil {
			return nil, err
		}
		r.Set("members", members)
		r.Set("pair", pairKey(members))
		err = app.Save(r)
	}
	if err != nil {
		return nil, err
	}
	for _, id := range members {
		if _, err := findMembership(app, r.Id, id); err != nil {
			return nil, err
		}
	}
	return r, nil
}

func findMembership(app core.App, conv, user string) (*core.Record, error) {
	row, err := app.FindFirstRecordByFilter("conversation_members", "conversation = {:c} && user = {:u}", dbx.Params{"c": conv, "u": user})
	if err == nil {
		return row, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	row, err = newRow(app, "conversation_members")
	if err != nil {
		return nil, err
	}
	row.Set("conversation", conv)
	row.Set("user", user)
	return row, app.Save(row)
}
