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

	dbus "github.com/godbus/dbus/v5"
)

type Metadata struct {
	Album       string
	AlbumArtist []string
	Url         string
	Artist      []string
	Composer    []string
	TrackId     string
	Title       string
}

var ErrMetadataFailed = errors.New("failed to get metadata")
var ErrInvalidType = errors.New("invalid type for field")
var ErrInvalidSignalBody = errors.New("invalid body for signal")
var ErrNoStatus = errors.New("player's new metadata has no status")

var filteredPlayers = []string{
	"playerctld",
}

// Track bus names so filtered players can be sorted out
var busNameToName = make(map[string]string)
var nameToBusName = make(map[string]string)

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

	// TODO: Actually make use of this context
	ctx := conn.Context()

	slog.InfoContext(ctx, "Starting monitor of DBus")

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

func handleNewPlayer(ctx context.Context, conn *dbus.Conn, sig *dbus.Signal, callback StoreCallback) error {
	if len(sig.Body) != 3 {
		// Should be name, oldOwner, newOwner
		return ErrInvalidSignalBody
	}
	// This is the player name
	name, nameOk := sig.Body[0].(string)
	newOwner, newOk := sig.Body[0].(string)
	oldOwner, oldOk := sig.Body[0].(string)
	if !nameOk || !newOk || !oldOk {
		return ErrInvalidSignalBody
	}
	// A new player connecting will send two signals:
	// One for the bus (:1.<bus-num>) and one for the name we want (org.mpris.MediaPlayer2.*)
	if strings.HasPrefix(name, "org.mpris.MediaPlayer2.") {
		if oldOwner == newOwner {
			// Connected
			addPlayer(conn, name)
			if isFilteredPlayer(name) {
				slog.Debug("Ignoring filtered player", "Player", name)
				return nil
			}
			metadata, err := GetMetadata(conn.Object(name, dbus.ObjectPath(playerPath)))
			if err != nil {
				return err
			}
			return callback(ctx, metadata)
		} else {
			// Disconnected
			removePlayer(name)
		}
	}
	return nil
}

func handlePropertyChange(ctx context.Context, sig *dbus.Signal, callback StoreCallback) error {
	bus := sig.Sender // This is the bus name
	name, ok := busNameToName[bus]
	if ok {
		if isFilteredPlayer(name) {
			slog.Debug("Ignoring filtered player", "Name", name)
			return nil
		}
	} else {
		slog.Warn("Received signal from unknown player, ignoring", "Bus", bus)
		// TODO: This can go if we ever scan for all currently existing players at startup
		return nil
	}
	slog.Debug("Detected change in player", "Name", name, "Bus", bus)
	if len(sig.Body) < 1 {
		return ErrInvalidSignalBody
	}
	changed, ok := sig.Body[1].(map[string]dbus.Variant)
	if !ok {
		return ErrInvalidSignalBody
	}
	// Only the property that changed will show up here
	// E.g. only "PlaybackStatus" or "Metadata"
	// "Metadata" and "PlaybackStatus" both show up when MPV exits
	if _, ok := changed["PlaybackStatus"]; ok {
		// This is a new player connecting, resuming, etc. We don't care about this
		return nil
	}

	_m, ok := changed["Metadata"]
	if !ok {
		return ErrMetadataFailed
	}
	metadata, ok := _m.Value().(map[string]dbus.Variant)
	if !ok {
		slog.Debug("Received invalid type for metadata", "Name", name, "Bus", bus)
		return ErrMetadataFailed
	}
	return callback(ctx, parseMetadata(metadata))
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

func isFilteredPlayer(serviceName string) bool {
	for _, name := range filteredPlayers {
		if strings.HasSuffix(serviceName, name) {
			return true
		}
	}
	return false
}

func addPlayer(conn *dbus.Conn, name string) error {
	// Get the bus name for the player
	systemBus := conn.Object(systemBusName, systemBusPath)
	call := systemBus.Call(systemBusName+".GetNameOwner", 0, name)
	if call.Err != nil {
		return call.Err
	}
	var busName string
	if err := call.Store(&busName); err != nil {
		return err
	}
	busNameToName[busName] = name
	nameToBusName[name] = busName
	return nil
}

func removePlayer(name string) {
	busName, ok := nameToBusName[name]
	if !ok {
		slog.Warn("Attempted to remove player not in mapping", "Name", name)
		// Try to find by iterating
		for bus, n := range busNameToName {
			if n == name {
				delete(busNameToName, bus)
				break
			}
		}
	} else if _, ok := busNameToName[busName]; ok {
		// Remove both mappings
		delete(busNameToName, busName)
		delete(nameToBusName, name)
	} else {
		// Only in this mapping
		slog.Warn("Found player in name -> bus but not bus -> name", "Name", name, "Bus", busName)
		delete(nameToBusName, name)
	}
}
