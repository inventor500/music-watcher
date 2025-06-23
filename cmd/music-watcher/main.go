package main

import (
	"context"
	"database/sql"
	"log/slog"
	"os"

	dbus "github.com/godbus/dbus/v5"
	music "github.com/inventor500/music-watcher"
	_ "github.com/mattn/go-sqlite3"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))
	slog.SetDefault(logger)
	dbusConn, err := dbus.SessionBus()
	if err != nil {
		os.Exit(1)
	}
	defer dbusConn.Close()
	db, err := sql.Open("sqlite3", "file:test.db")
	if err != nil {
		os.Exit(1)
	}
	music.StartWatching(dbusConn, func(ctx context.Context, m *music.Metadata) error {
		err := music.StoreData(ctx, m, db)
		if err != nil {
			slog.ErrorContext(ctx, "Failed to store value", "Error", err)
		}
		return err
	})
}
