package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	dbus "github.com/godbus/dbus/v5"
	music "github.com/inventor500/music-watcher"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))
	slog.SetDefault(logger)
	conn, err := dbus.SessionBus()
	if err != nil {
		os.Exit(1)
	}
	defer conn.Close()
	music.StartWatching(conn, func (ctx context.Context, m *music.Metadata) error {
		fmt.Printf("Received metadata %s\n", m)
		return nil
	})
}

	// call := conn.Object("org.mpris.MediaPlayer2.mpv", dbus.ObjectPath(playerPath)).
	// 	Call(propertiesInterface+".Get", 0, playerInterface, "Metadata")

	// if call.Err != nil {
	// 	log.Fatalf("Failed to get metadata: %v", call.Err)
	// }

	// var metadata map[string]dbus.Variant
	// if err := call.Store(&metadata); err != nil {
	// 	log.Fatalf("Failed to parse metadata: %v", err)
	// }

	// for key, value := range metadata {
	// 	fmt.Printf("%s: %v\n", key, value.Value())
	// }
	// }
