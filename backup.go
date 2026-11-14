package main

import (
	"context"
	"strings"
	"time"
)

func (s *service) backup() {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Minute)
	defer cancel()
	name := "daily-" + time.Now().UTC().Format("2006-01-02") + ".zip"
	if err := s.app.CreateBackup(ctx, name); err != nil {
		s.app.Logger().Error("Daily backup failed", "error", err)
		return
	}
	store, err := s.app.NewBackupsFilesystem()
	if err != nil {
		s.app.Logger().Error("Backup retention failed", "error", err)
		return
	}
	defer store.Close()
	list, err := store.List("")
	if err != nil {
		s.app.Logger().Error("Backup retention failed", "error", err)
		return
	}
	cutoff := time.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, -6)
	for _, item := range list {
		day, ok := strings.CutPrefix(item.Key, "daily-")
		if !ok {
			continue
		}
		day, ok = strings.CutSuffix(day, ".zip")
		if !ok {
			continue
		}
		date, err := time.Parse("2006-01-02", day)
		if err != nil || !date.Before(cutoff) {
			continue
		}
		if err := store.Delete(item.Key); err != nil {
			s.app.Logger().Warn("Backup deletion failed", "archive", item.Key, "error", err)
		}
	}
}
