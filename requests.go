package main

import (
	"database/sql"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

const requestNoteLimit = 500

func init() { m.Register(messageRequestSchema, nil, "1794686400_message_requests.go") }

func messageRequestSchema(app core.App) error {
	users, err := app.FindCollectionByNameOrId("users")
	if err != nil {
		return err
	}
	sender := relation("sender", users, true)
	sender.CascadeDelete = true
	recipient := relation("recipient", users, true)
	recipient.CascadeDelete = true
	requests := collection("message_requests", sender, recipient,
		&core.TextField{Name: "pair", Required: true, Max: 32, Hidden: true},
		&core.TextField{Name: "note", Max: requestNoteLimit})
	requests.ListRule = ptr("@request.auth.id != '' && (sender = @request.auth.id || recipient = @request.auth.id)")
	requests.ViewRule = requests.ListRule
	requests.AddIndex("idx_message_request_pair", true, "pair", "")
	requests.AddIndex("idx_message_request_recipient", false, "recipient,created", "")
	requests.AddIndex("idx_message_request_sender", false, "sender,created", "")
	return app.Save(requests)
}

type memberProfile struct {
	ID     string `json:"id" db:"id"`
	Name   string `json:"name" db:"name"`
	Avatar string `json:"avatar,omitempty"`
}

type inboxChat struct {
	ID   string         `json:"id"`
	Peer *memberProfile `json:"peer"`
}

type inboxRequest struct {
	ID       string         `json:"id"`
	Peer     *memberProfile `json:"peer"`
	Incoming bool           `json:"incoming"`
	Note     string         `json:"note"`
}

func (s *service) inbox(e *core.RequestEvent) error {
	user := e.Auth.Id
	conversations, err := s.app.FindRecordsByFilter("conversations", "members.id ?= {:u}", "-created,-id", 0, 0, dbx.Params{"u": user})
	if err != nil {
		return apiError(e, err)
	}
	requests, err := s.app.FindRecordsByFilter("message_requests", "sender = {:u} || recipient = {:u}", "-created,-id", 0, 0, dbx.Params{"u": user})
	if err != nil {
		return apiError(e, err)
	}
	chats := make([]inboxChat, 0, len(conversations))
	pending := make([]inboxRequest, 0, len(requests))
	profiles := make(map[string]*memberProfile)
	for _, chat := range conversations {
		for _, peer := range chat.GetStringSlice("members") {
			if peer != user {
				profile := profiles[peer]
				if profile == nil {
					profile = &memberProfile{ID: peer}
					profiles[peer] = profile
				}
				chats = append(chats, inboxChat{ID: chat.Id, Peer: profile})
				break
			}
		}
	}
	for _, request := range requests {
		peer, incoming := request.GetString("recipient"), request.GetString("recipient") == user
		if incoming {
			peer = request.GetString("sender")
		}
		profile := &memberProfile{ID: peer}
		if existing := profiles[peer]; existing != nil {
			profile = existing
		} else {
			profiles[peer] = profile
		}
		pending = append(pending, inboxRequest{ID: request.Id, Peer: profile, Incoming: incoming, Note: request.GetString("note")})
	}
	ids := make([]string, 0, len(profiles))
	for id := range profiles {
		ids = append(ids, id)
	}
	if len(ids) > 0 {
		records, err := s.app.FindRecordsByIds("users", ids, nil)
		if err != nil {
			return apiError(e, err)
		}
		for _, record := range records {
			profiles[record.Id].Name = record.GetString("name")
			profiles[record.Id].Avatar = record.GetString("avatar")
		}
	}
	for i := range pending {
		profile := pending[i].Peer
		pending[i].Peer = &memberProfile{ID: profile.ID, Name: profile.Name}
	}
	return e.JSON(200, map[string]any{"chats": chats, "requests": pending})
}

func (s *service) searchMembers(e *core.RequestEvent) error {
	query := strings.TrimSpace(e.Request.URL.Query().Get("q"))
	size := utf8.RuneCountInString(query)
	if size < 2 || size > 255 {
		return e.BadRequestError("Enter a full email address or at least two characters of a name.", nil)
	}
	condition := "email = {:query} COLLATE NOCASE"
	if strings.Contains(query, "@") {
		query = strings.ToLower(query)
	} else {
		condition = "name LIKE {:query} ESCAPE '\\' COLLATE NOCASE"
		query = "%" + strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(query) + "%"
	}
	var results []memberProfile
	err := s.app.DB().Select("id", "name").From("users").
		Where(dbx.NewExp("id != {:user} AND ("+condition+")", dbx.Params{"user": e.Auth.Id, "query": query})).
		OrderBy("name", "id").Limit(20).All(&results)
	if err != nil {
		return apiError(e, err)
	}
	if results == nil {
		results = []memberProfile{}
	}
	return e.JSON(200, results)
}

func (s *service) sendMessageRequest(e *core.RequestEvent) error {
	var body struct {
		Recipient string `json:"recipient"`
		Note      string `json:"note"`
	}
	if e.BindBody(&body) != nil || body.Recipient == "" || body.Recipient == e.Auth.Id {
		return apiError(e, errInvalid)
	}
	body.Note = strings.TrimSpace(body.Note)
	if utf8.RuneCountInString(body.Note) > requestNoteLimit {
		return e.BadRequestError("The first message can contain up to 500 characters.", nil)
	}
	result := map[string]string{}
	status := 200
	err := s.app.RunInTransaction(func(tx core.App) error {
		members := []string{e.Auth.Id, body.Recipient}
		chat, err := findConversation(tx, members)
		if err == nil {
			result["conversation"] = chat.Id
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		pair := pairKey(members)
		row, err := tx.FindFirstRecordByData("message_requests", "pair", pair)
		if errors.Is(err, sql.ErrNoRows) {
			if _, err := tx.FindRecordById("users", body.Recipient); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					return errMissing
				}
				return err
			}
			row, err = newRow(tx, "message_requests")
			if err != nil {
				return err
			}
			row.Set("pair", pair)
			row.Set("sender", e.Auth.Id)
			row.Set("recipient", body.Recipient)
			row.Set("note", body.Note)
			if err := tx.Save(row); err != nil {
				return err
			}
			status = 201
		} else if err != nil {
			return err
		}
		result["request"] = row.Id
		if row.GetString("recipient") == e.Auth.Id {
			result["direction"] = "incoming"
		} else {
			result["direction"] = "outgoing"
		}
		return nil
	})
	if err != nil {
		return apiError(e, err)
	}
	return e.JSON(status, result)
}

func (s *service) acceptMessageRequest(e *core.RequestEvent) error {
	var chat *core.Record
	err := s.app.RunInTransaction(func(tx core.App) error {
		row, err := tx.FindRecordById("message_requests", e.Request.PathValue("id"))
		if errors.Is(err, sql.ErrNoRows) || err == nil && row.GetString("recipient") != e.Auth.Id {
			return errMissing
		}
		if err != nil {
			return err
		}
		sender := row.GetString("sender")
		chat, err = ensureConversation(tx, []string{sender, e.Auth.Id})
		if err != nil {
			return err
		}
		if note := row.GetString("note"); note != "" {
			message, err := newRow(tx, "messages")
			if err != nil {
				return err
			}
			message.Set("conversation", chat.Id)
			message.Set("sender", sender)
			message.Set("kind", "text")
			message.Set("text", note)
			message.Set("client_message_id", "request:"+row.Id)
			if err := tx.Save(message); err != nil {
				return err
			}
		}
		return tx.Delete(row)
	})
	if err != nil {
		return apiError(e, err)
	}
	return e.JSON(200, map[string]string{"conversation": chat.Id})
}

func (s *service) removeMessageRequest(e *core.RequestEvent) error {
	err := s.app.RunInTransaction(func(tx core.App) error {
		row, err := tx.FindRecordById("message_requests", e.Request.PathValue("id"))
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if row.GetString("sender") != e.Auth.Id && row.GetString("recipient") != e.Auth.Id {
			return errMissing
		}
		return tx.Delete(row)
	})
	if err != nil {
		return apiError(e, err)
	}
	return e.NoContent(204)
}
