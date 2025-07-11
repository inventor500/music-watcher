package music_watch

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"time"
)

var ErrInvalidAlbumName = errors.New("invalid album name")

// Store the record in the database, creating entries as necessary
func StoreData(
	ctx context.Context,
	data *Metadata,
	conn interface {
		BeginTx(ctx context.Context, opts *sql.TxOptions) (*sql.Tx, error)
	},
) error {
	if len(data.Title) == 0 && len(data.Url) == 0 {
		slog.Info("Received track with no title or url")
		return nil
	}
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	now := time.Now().Format(time.DateTime)
	trackIdNumber, err := getTrack(ctx, tx, data.Title, data.TrackId, data.Url, data.Album, [][]string{data.AlbumArtist, data.Artist, data.Composer})
	if err != nil {
		tx.Rollback()
		return err
	}
	_, err = tx.ExecContext(
		ctx,
		"INSERT INTO TrackLog (track, timestamp) VALUES (?, ?)",
		trackIdNumber,
		now,
	)
	if err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}

// Create a mapping for track <-> person, creating person if necessary
func addPerson(ctx context.Context, tx *sql.Tx, trackId int64, person string) error {
	// trackId here is the database ID number of the track
	if len(person) == 0 {
		return nil
	}
	var personId int64
	err := tx.QueryRowContext(ctx, "SELECT id FROM Person WHERE name = ?", person).Scan(&personId)
	switch err {
	case sql.ErrNoRows:
		res, err := tx.ExecContext(ctx, "INSERT INTO Person (name) VALUES (?)", person)
		if err != nil {
			return err
		}
		personId, err = res.LastInsertId()
		if err != nil {
			return err
		}
	case nil: // Use the existing record
	default:
		return err
	}
	_, err = tx.ExecContext(
		ctx,
		"INSERT INTO Track_Person (track, person) VALUES (?, ?)",
		trackId,
		personId,
	)
	return err
}

// Get the track id, creating the record if necessary
func getTrack(ctx context.Context, tx *sql.Tx, title, trackId, url, album string, persons [][]string) (int64, error) {
	// trackId parameter is the string uniquely identifying the track to the music industry, not our database
	// Because trackId is often not present, (url, title) should uniquely identify the track
	var id int64
	err := tx.QueryRowContext(ctx, "SELECT id FROM Track WHERE url = ? AND title = ?", url, title).Scan(&id)
	switch err {
	case sql.ErrNoRows:
		// Create record
		if len(album) > 0 {
			alb, err := getAlbum(ctx, tx, album)
			if err != nil {
				slog.ErrorContext(ctx, "Error inserting album into database", "Album", album, "Track", title, "Error", err)
				return 0, err
			}
			res, err := tx.ExecContext(
				ctx,
				"INSERT INTO Track (title, trackId, url, album) VALUES (?, ?, ?, ?)",
				title,
				trackId,
				url,
				alb,
			)
			if err != nil {
				return 0, err
			}
			if id, err := res.LastInsertId(); err == nil {
				err = insertPersons(ctx, tx, id, persons)
				return id, err
			} else {
				return 0, err
			}
		} else { // No album specified
			res, err := tx.ExecContext(
				ctx,
				"INSERT INTO Track (title, trackId, url) VALUES (?, ?, ?)",
				title,
				trackId,
				url,
			)
			if err != nil {
				return 0, err
			}
			if id, err := res.LastInsertId(); err == nil {
				return id, insertPersons(ctx, tx, id, persons)
			} else {
				return 0, err
			}
		}
	case nil:
		return id, nil
	default:
		return 0, err
	}
}

func insertPersons(ctx context.Context, tx *sql.Tx, trackId int64, persons [][]string) error {
	var artSet = make(artistSet)
	for _, set := range persons {
		for _, person := range set {
			// Check if the person has been seen before
			// Some songs have the same person listed as, e.g., the artist and the composer
			if _, ok := artSet[person]; ok {
				continue
			} else {
				artSet[person] = struct{}{}
			}
			// Could potentially add 2 records - One to "Person" and one to "Album_Person"
			err := addPerson(ctx, tx, trackId, person)
			if err != nil {
				tx.Rollback()
				return err
			}
		}
	}
	return nil
}

// Get the album ID, or insert it if it does not already exist
func getAlbum(ctx context.Context, tx *sql.Tx, name string) (int64, error) {
	if len(name) == 0 {
		return 0, ErrInvalidAlbumName
	}
	var id int64
	err := tx.QueryRowContext(ctx, "SELECT id FROM Album WHERE id = ?", id).Scan(&id)
	switch err {
	case sql.ErrNoRows:
		// Create the album entry
		res, err := tx.ExecContext(ctx, "INSERT INTO Album (title) VALUES (?)", name)
		if err != nil {
			return 0, err
		}
		return res.LastInsertId()
	case nil:
		// No error
		return id, nil
	default:
		// Unknown error
		return 0, err
	}
}

func CreateDatabaseStructure(conn *sql.DB) error {
	tx, err := conn.Begin()
	if err != nil {
		return err
	}
	for _, stmt := range []string{
		"CREATE TABLE IF NOT EXISTS Album (id INTEGER PRIMARY KEY, title TEXT)",
		"CREATE TABLE IF NOT EXISTS Track (id INTEGER PRIMARY KEY, title TEXT, trackId TEXT, url TEXT, album INTEGER)",
		"CREATE TABLE IF NOT EXISTS Person(id INTEGER PRIMARY KEY, name TEXT)",
		"CREATE TABLE IF NOT EXISTS TrackLog (id INTEGER PRIMARY KEY, track INTEGER, timestamp DATETIME)",
		"CREATE TABLE IF NOT EXISTS Track_Person(id INTEGER PRIMARY KEY, track INTEGER, person INTEGER)",
	} {
		_, err := tx.Exec(stmt)
		if err != nil {
			tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

type artistSet map[string]struct{}
