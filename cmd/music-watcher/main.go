package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"log"
	"log/slog"
	"os"
	"path/filepath"

	dbus "github.com/godbus/dbus/v5"
	music "github.com/inventor500/music-watcher"
	_ "github.com/mattn/go-sqlite3"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))
	slog.SetDefault(logger)
	args, err := parseArgs()
	if err != nil {
		log.Fatalf("Unable to parse arguments: %s\n", err)
	}
	dbusConn, err := dbus.SessionBus()
	if err != nil {
		os.Exit(1)
	}
	defer dbusConn.Close()
	db, err := createDB(args.DBPath)
	if err != nil {
		log.Fatalf("Unable to open database: %s", err)
	}
	defer db.Close()
	music.StartWatching(dbusConn, func(ctx context.Context, m *music.Metadata) error {
		err := music.StoreData(ctx, m, db)
		if err != nil {
			slog.ErrorContext(ctx, "Failed to store value", "Error", err)
		}
		return err
	})
}

type Arguments struct {
	DBPath string
}

func parseArgs() (*Arguments, error) {
	var args Arguments
	flag.StringVar(&args.DBPath, "dbpath", defaultDBPath(), "The location of the database file.")
	flag.Parse()
	unused := flag.Args()
	if len(unused) > 0 {
		return nil, fmt.Errorf("received too many arguments: %v", unused)
	}
	return &args, nil
}

func defaultDBPath() string {
	xdgPath, ok := os.LookupEnv("XDG_DATA_HOME")
	if !ok {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		xdgPath = filepath.Join(home, ".local/share")
	}
	if !testDir(xdgPath) {
		return ""
	}
	configPath := filepath.Join(xdgPath, "music-watcher")
	if !testDir(configPath) {
		return ""
	}
	return filepath.Join(configPath, "data.db")
}

func testDir(path string) bool {
	if stat, err := os.Stat(path); err == nil && stat.IsDir() {
		return true
	}
	return false
}

func createDB(path string) (*sql.DB, error) {
	if len(path) == 0 {
		xdgPath, ok := os.LookupEnv("XDG_DATA_HOME")
		if !ok {
			home, err := os.UserHomeDir()
			if err != nil {
				return nil, err
			}
			xdgPath = filepath.Join(home, ".local/share")
		}
		if stat, err := os.Stat(xdgPath); err != nil {
			return nil, err
		} else if !stat.IsDir() {
			return nil, fmt.Errorf("XDG_DATA_HOME directory (%s) is not a directory", xdgPath)
		}
		path = filepath.Join(xdgPath, "music-watcher", "data.db")
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0750); err != nil {
		if !os.IsExist(err) {
			return nil, err
		}
	}
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		return nil, err
	}
	if err := music.CreateDatabaseStructure(db); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}
