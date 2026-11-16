package main

import (
	"context"
	"log"
	"net"
	"net/http"
	"net/netip"
	"os"
	"strings"
	"time"

	lk "github.com/livekit/server-sdk-go/v2"
	proxyproto "github.com/pires/go-proxyproto"
	"github.com/pocketbase/pocketbase"
	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"
)

type service struct {
	app                core.App
	room               *lk.RoomServiceClient
	key, secret, wsURL string
	push               pushDelivery
}

func main() {
	app := pocketbase.New()
	cfg := &service{
		app:    app,
		key:    os.Getenv("LIVEKIT_API_KEY"),
		secret: os.Getenv("LIVEKIT_API_SECRET"),
		wsURL:  os.Getenv("LIVEKIT_WS_URL"),
	}
	app.OnServe().BindFunc(func(e *core.ServeEvent) error {
		if webDir := os.Getenv("APP_WEB_DIR"); webDir != "" {
			web := apis.Static(os.DirFS(webDir), true)
			e.Router.GET("/{path...}", func(e *core.RequestEvent) error {
				if strings.HasPrefix(e.Request.URL.Path, "/api/") {
					return e.NotFoundError("", nil)
				}
				e.Response.Header().Set("Referrer-Policy", "no-referrer")
				if strings.HasPrefix(e.Request.URL.Path, "/assets/") {
					e.Response.Header().Set("Cache-Control", "public,max-age=31536000,immutable")
				} else {
					e.Response.Header().Set("Cache-Control", "no-cache")
				}
				return web(e)
			})
		}
		if cidr := os.Getenv("APP_PROXY_CIDR"); cidr != "" {
			prefix, err := netip.ParsePrefix(cidr)
			if err != nil {
				return err
			}
			delegate, err := net.Listen("tcp", e.Server.Addr)
			if err != nil {
				return err
			}
			e.Listener = &proxyproto.Listener{
				Listener:          delegate,
				ReadHeaderTimeout: 3 * time.Second,
				ConnPolicy: func(opts proxyproto.ConnPolicyOptions) (proxyproto.Policy, error) {
					if addr, ok := opts.Upstream.(*net.TCPAddr); ok && prefix.Contains(addr.AddrPort().Addr().Unmap()) {
						return proxyproto.USE, nil
					}
					return proxyproto.REJECT, nil
				},
			}
			e.Server.Protocols = new(http.Protocols)
			e.Server.Protocols.SetHTTP1(true)
			e.Server.Protocols.SetUnencryptedHTTP2(true)
		}
		if cfg.key != "" && cfg.secret != "" && cfg.wsURL != "" {
			cfg.room = lk.NewRoomServiceClient(env("LIVEKIT_INTERNAL_URL", "http://livekit:7880"), cfg.key, cfg.secret)
		}
		if err := cfg.push.init(context.Background()); err != nil {
			return err
		}
		if err := cfg.configure(); err != nil {
			return err
		}
		if err := cfg.bootstrap(); err != nil {
			return err
		}

		g := e.Router.Group("/api/app")
		cfg.accountRoutes(e)
		g.GET("/health", cfg.health)
		g.POST("/livekit/webhook", cfg.livekitWebhook)
		auth := g.Group("").Bind(apis.RequireAuth("users"))
		auth.GET("/inbox", cfg.inbox)
		auth.GET("/members/search", cfg.searchMembers)
		auth.POST("/message-requests", cfg.sendMessageRequest).Bind(apis.BodyLimit(8192))
		auth.POST("/message-requests/{id}/accept", cfg.acceptMessageRequest)
		auth.DELETE("/message-requests/{id}", cfg.removeMessageRequest)
		auth.POST("/calls", cfg.startCall)
		auth.POST("/calls/{id}/accept", cfg.changeCall("accept"))
		auth.POST("/calls/{id}/reject", cfg.changeCall("reject"))
		auth.POST("/calls/{id}/end", cfg.changeCall("end"))
		auth.POST("/calls/{id}/token", cfg.callToken)
		auth.POST("/messages", cfg.sendMessage).Bind(apis.BodyLimit(51 << 20))
		auth.PATCH("/messages/{id}", cfg.editMessage)
		auth.DELETE("/messages/{id}", cfg.deleteMessage)
		auth.PUT("/reactions/{id}", cfg.react)
		auth.PUT("/conversations/{id}/read", cfg.readCursor)
		auth.PUT("/conversations/{id}/delivered", cfg.deliveredCursor)
		auth.PUT("/conversations/{id}/presence", cfg.presence)
		auth.GET("/conversations/{id}/people", cfg.people)
		auth.POST("/devices", cfg.registerDevice)
		auth.DELETE("/devices/{id}", cfg.removeDevice)
		auth.GET("/push", cfg.pushInfo)
		app.Cron().MustAdd("notifications", "*/1 * * * *", cfg.maintain)
		app.Cron().MustAdd("backup", "0 20 * * *", cfg.backup)
		ctx, stop := context.WithCancel(context.Background())
		app.OnTerminate().BindFunc(func(e *core.TerminateEvent) error { stop(); return e.Next() })
		go func() {
			ticker := time.NewTicker(3 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					cfg.processOutbox(ctx)
				}
			}
		}()
		return e.Next()
	})
	if err := app.Start(); err != nil {
		log.Fatal(err)
	}
}

func env(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func ptr(s string) *string { return &s }

func (s *service) configure() error {
	settings := s.app.Settings()
	s.mailConfig(settings)
	settings.Meta.AppURL = env("APP_URL", "http://127.0.0.1:8090")
	settings.Logs.MaxDays = 7
	settings.RateLimits.Enabled = true
	settings.TrustedProxy.Headers = nil
	return s.app.Save(settings)
}

func (s *service) health(e *core.RequestEvent) error {
	if _, err := s.app.FindCollectionByNameOrId("calls"); err != nil {
		return e.JSON(503, map[string]any{"status": "unavailable"})
	}
	return e.JSON(200, map[string]any{"status": "ok", "callingConfigured": s.room != nil})
}

func (s *service) bootstrap() error {
	email, password := os.Getenv("BOOTSTRAP_ADMIN_EMAIL"), os.Getenv("BOOTSTRAP_ADMIN_PASSWORD")
	if email != "" && password != "" {
		if _, err := s.app.FindAuthRecordByEmail(core.CollectionNameSuperusers, email); err != nil {
			col, err := s.app.FindCollectionByNameOrId(core.CollectionNameSuperusers)
			if err != nil {
				return err
			}
			admin := core.NewRecord(col)
			admin.SetEmail(email)
			admin.SetPassword(password)
			if err := s.app.Save(admin); err != nil {
				return err
			}
		}
	}
	accounts := []string{os.Getenv("BOOTSTRAP_USER1_EMAIL"), os.Getenv("BOOTSTRAP_USER2_EMAIL")}
	if accounts[0] == "" && accounts[1] == "" {
		return nil
	}
	if accounts[0] == "" || accounts[1] == "" || strings.EqualFold(accounts[0], accounts[1]) {
		return os.ErrInvalid
	}
	return s.app.RunInTransaction(func(tx core.App) error {
		users, err := tx.FindCollectionByNameOrId("users")
		if err != nil {
			return err
		}
		var ids []string
		for i, email := range accounts {
			person, err := tx.FindAuthRecordByEmail("users", email)
			if err != nil {
				person = core.NewRecord(users)
				person.SetEmail(email)
				key := []string{"BOOTSTRAP_USER1_PASSWORD", "BOOTSTRAP_USER2_PASSWORD"}[i]
				if os.Getenv(key) == "" {
					return os.ErrInvalid
				}
				person.SetPassword(os.Getenv(key))
				person.Set("name", []string{"Member 1", "Member 2"}[i])
				if err := tx.Save(person); err != nil {
					return err
				}
			}
			ids = append(ids, person.Id)
		}
		_, err = ensureConversation(tx, ids)
		return err
	})
}
