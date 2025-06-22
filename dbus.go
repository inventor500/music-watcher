package music_watch

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/godbus/dbus/v5"
)

var ErrMetadataFailed = errors.New("failed to get metadata")
var ErrInvalidType = errors.New("invalid type for field")
var ErrInvalidSignalBody = errors.New("invalid body for signal")
var ErrNoStatus = errors.New("player's new metadata has no status")

const playerPath = "/org/mpris/MediaPlayer2"
const systemBusPath = "/org/freedesktop/DBus"
const systemBusName = "org.freedesktop.DBus"
const propertiesChangedName = "org.freedesktop.DBus.Properties"
const ownerChanged = "org.freedesktop/DBus.NameOwnerChanged"
const introspectName = "org.freedesktop.DBus.Introspectable.Introspect"
const nameOwnerSignal = "org.freedesktop.DBus.NameOwnerChanged"
const propertySignal = "org.freedesktop.DBus.Properties.PropertiesChanged"

type StoreCallback func(ctx context.Context, m *Metadata) error

func StartWatching(conn *dbus.Conn, callback StoreCallback) error {

	// TODO: Get player in loop
	ctx := conn.Context()

	// New players
	if err := conn.AddMatchSignalContext(
		ctx,
		dbus.WithMatchObjectPath(systemBusPath),
		dbus.WithMatchInterface(systemBusName),
		dbus.WithMatchMember("NameOwnerChanged"),
	); err != nil {
		return err
	}

	// Property changes
	if err := conn.AddMatchSignalContext(
		ctx,
		dbus.WithMatchInterface(propertiesChangedName),
		dbus.WithMatchMember("PropertiesChanged"),
		dbus.WithMatchArg(0, "org.mpris.MediaPlayer2.Player"),
	); err != nil {
		return err
	}

	// DBus changes
	dbusChan := make(chan *dbus.Signal)
	conn.Signal(dbusChan)

	// Handle OS signals to stop
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	for {
		select {
		case sig := <-dbusChan:
			switch sig.Name {
			case nameOwnerSignal:
				handleNewPlayer(ctx, conn, sig, callback)
			case propertySignal:
				handlePropertyChange(ctx, sig, callback)
			}
		case <-sigChan:
			slog.InfoContext(ctx, "Received shutdown signal")
			return nil
		}
	}
}

// signal := make(chan *dbus.Signal, 10)
// conn.Signal(signal)
// for sig := range signal {

// 	if sig.Name == ownerChanged {
// 		if len(sig.Body) < 1 {
// 			slog.ErrorContext(ctx, "Invalid signal body length", "Length", len(sig.Body))
// 			continue
// 		}
// 		switch name := sig.Body[0].(type) {
// 		case string:
// 			handleNewPlayer(ctx, conn, name)
// 		default:
// 			slog.ErrorContext(ctx, "Received unknown type for signal body")
// 			continue
// 		}
// 	}
// }
// player := "org.mpris.MediaPlayer2.mpv"
// metadata, err := GetMetadata(conn.Object(player, dbus.ObjectPath(playerPath)))
// if err != nil {
// 	slog.Error("Failed to get metadata", "Player", player)
// }
// slog.Info("Received metadata", "Metadata", metadata)
// 	return nil
// }

func handleNewPlayer(ctx context.Context, conn *dbus.Conn, sig *dbus.Signal, callback StoreCallback) error {
	if len(sig.Body) != 3 {
		// Should be name, oldOwner, newOwner
		return ErrInvalidSignalBody
	}
	name, nameOk := sig.Body[0].(string)
	if !nameOk {
		return ErrInvalidSignalBody
	}
	if strings.HasPrefix(name, "org.mpris.MediaPlayer2.") {
		// Client connected
		metadata, err := GetMetadata(conn.Object(name, dbus.ObjectPath(playerPath)))
		if err != nil {
			return err
		}
		return callback(ctx, metadata)
	}
	return nil
}

func handlePropertyChange(ctx context.Context, sig *dbus.Signal, callback StoreCallback) error {
	name := sig.Sender
	slog.Debug("Detected change in player", "Name", name)
	if len(sig.Body) < 1 {
		return ErrInvalidSignalBody
	}
	changed, ok := sig.Body[1].(map[string]dbus.Variant)
	if !ok {
		return ErrInvalidSignalBody
	}
	status, hasStatus := changed["PlaybackStatus"]
	if statusText, ok := status.Value().(string); hasStatus && ok && statusText != "Playing" {
		slog.DebugContext(ctx, "Player has no status or status is not playing", "Name", name, "StatusText", statusText)
		return ErrNoStatus
	}
	// We only care if this is playing
	_m, ok := changed["Metadata"]
	if !ok {
		return ErrMetadataFailed
	}
	metadata, ok := _m.Value().(map[string]dbus.Variant)
	if !ok {
		slog.Debug("Received invalid type for metadata", "Name", name)
		return ErrMetadataFailed
	}
	return callback(ctx, parseMetadata(metadata))
}

type Metadata struct {
	Album       string
	AlbumArtist []string
	Url         string
	Artist      []string
	Composer    []string
	TrackId     string
	Title       string
}

func (m *Metadata) String() string {
	return fmt.Sprintf("Album: %s; Title: %s", m.Album, m.Title)
}

func GetMetadata(player dbus.BusObject) (*Metadata, error) {
	const propertiesInterface = "org.freedesktop.DBus.Properties"
	const playerInterface = "org.mpris.MediaPlayer2.Player"
	call := player.Call(propertiesInterface+".Get", 0, playerInterface, "Metadata")
	if call.Err != nil {
		return nil, errors.Join(ErrMetadataFailed, call.Err)
	}
	var meta map[string]dbus.Variant
	if err := call.Store(&meta); err != nil {
		return nil, errors.Join(ErrMetadataFailed, err)
	}
	return parseMetadata(meta), nil
}

func parseMetadata(metaMap map[string]dbus.Variant) *Metadata {
	var metadata Metadata
	for key, val := range metaMap {
		// If anyting here fails, just use the default value
		switch key {
		case "xesam:album":
			metadata.Album, _ = getAny[string](val)
		case "xesam:albumArtist":
			metadata.AlbumArtist, _ = getAny[[]string](val)
		case "xesam:url":
			metadata.Url, _ = getAny[string](val)
		case "xesam:artist":
			metadata.Artist, _ = getAny[[]string](val)
		case "xesam:composer":
			metadata.Composer, _ = getAny[[]string](val)
		case "mb:trackId":
			metadata.TrackId, _ = getAny[string](val)
		case "xesam:title":
			if temp, err := getAny[string](val); err != nil {
				slog.Warn("Failed to extract title from track, assuming blank")
			} else {
				metadata.Title = temp
			}
		}
	}
	return &metadata
}

func getAny[T any](value dbus.Variant) (T, error) {
	val := value.Value()
	if v, ok := val.(T); ok {
		return v, nil
	} else {
		var zeroVal T
		return zeroVal, ErrInvalidType
	}
}
