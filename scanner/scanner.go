package scanner

// this scanner tries to scan with a single unsorted walk of the music
// directory - which means you can come across the cover of an album/folder
// before the tracks (and therefore the album) which is an issue because
// when inserting into the album table, we need a reference to the cover.
// to solve this we're using godirwalk's PostChildrenCallback and some globals
//
// Album  -> needs a  CoverID
//        -> needs a  FolderID (American Football)
// Folder -> needs a  CoverID
//        -> needs a  ParentID
// Track  -> needs an AlbumID
//        -> needs a  FolderID

import (
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"path"
	"sync/atomic"
	"time"

	"github.com/jinzhu/gorm"
	"github.com/karrick/godirwalk"
	"github.com/pkg/errors"

	"github.com/sentriz/gonic/model"
)

var (
	IsScanning int32
)

type Scanner struct {
	db        *gorm.DB
	tx        *gorm.DB
	musicPath string
	// seenPaths is used to keep every path we've seen so that
	// we can remove old tracks, folders, and covers by path when we
	// are in the cleanDatabase stage
	seenPaths map[string]bool
	// currentDirStack is used for inserting to the folders (subsonic browse
	// by folder) which helps us work out a folder's parent
	currentDirStack dirStack
	// currentCover because we find a cover anywhere among the tracks during the
	// walk and need a reference to it when we update folder and album records
	// when we exit a folder
	currentCover *model.Cover
	// currentAlbum because we update this record when we exit a folder with
	// our new reference to it's cover
	currentAlbum *model.Album
}

func New(db *gorm.DB, musicPath string) *Scanner {
	return &Scanner{
		db:              db,
		musicPath:       musicPath,
		seenPaths:       make(map[string]bool),
		currentDirStack: make(dirStack, 0),
		currentCover:    &model.Cover{},
		currentAlbum:    &model.Album{},
	}
}

func (s *Scanner) updateAlbum(fullPath string, album *model.Album) {
	if s.currentAlbum.ID != 0 {
		return
	}
	directory, _ := path.Split(fullPath)
	// update album table (the currentAlbum record will be updated when
	// we exit this folder)
	err := s.tx.Where("path = ?", directory).First(s.currentAlbum).Error
	if !gorm.IsRecordNotFoundError(err) {
		// we found the record
		// TODO: think about mod time here
		return
	}
	s.currentAlbum = &model.Album{
		Path:          directory,
		Title:         album.Title,
		AlbumArtistID: album.AlbumArtistID,
		Year:          album.Year,
	}
	s.tx.Save(s.currentAlbum)
}

func (s *Scanner) handleCover(fullPath string, stat os.FileInfo) error {
	modTime := stat.ModTime()
	err := s.tx.Where("path = ?", fullPath).First(s.currentCover).Error
	if !gorm.IsRecordNotFoundError(err) &&
		modTime.Before(s.currentCover.UpdatedAt) {
		// we found the record but it hasn't changed
		return nil
	}
	image, err := ioutil.ReadFile(fullPath)
	if err != nil {
		return fmt.Errorf("when reading cover: %v", err)
	}
	s.currentCover = &model.Cover{
		Path:          fullPath,
		Image:         image,
		NewlyInserted: true,
	}
	s.tx.Save(s.currentCover)
	return nil
}

func (s *Scanner) handleFolder(fullPath string, stat os.FileInfo) error {
	// update folder table for browsing by folder
	folder := &model.Folder{}
	defer s.currentDirStack.Push(folder)
	modTime := stat.ModTime()
	err := s.tx.Where("path = ?", fullPath).First(folder).Error
	if !gorm.IsRecordNotFoundError(err) &&
		modTime.Before(folder.UpdatedAt) {
		// we found the record but it hasn't changed
		return nil
	}
	_, folderName := path.Split(fullPath)
	folder.Path = fullPath
	folder.ParentID = s.currentDirStack.PeekID()
	folder.Name = folderName
	s.tx.Save(folder)
	return nil
}

func (s *Scanner) handleFolderCompletion(fullPath string, info *godirwalk.Dirent) error {
	currentDir := s.currentDirStack.Peek()
	defer s.currentDirStack.Pop()
	var dirShouldSave bool
	if s.currentAlbum.ID != 0 {
		s.currentAlbum.CoverID = s.currentCover.ID
		s.tx.Save(s.currentAlbum)
		currentDir.HasTracks = true
		dirShouldSave = true
	}
	if s.currentCover.NewlyInserted {
		currentDir.CoverID = s.currentCover.ID
		dirShouldSave = true
	}
	if dirShouldSave {
		s.tx.Save(currentDir)
	}
	s.currentCover = &model.Cover{}
	s.currentAlbum = &model.Album{}
	log.Printf("processed folder `%s`\n", fullPath)
	return nil
}

func (s *Scanner) handleTrack(fullPath string, stat os.FileInfo, mime, exten string) error {
	//
	// set track basics
	track := &model.Track{}
	modTime := stat.ModTime()
	err := s.tx.Where("path = ?", fullPath).First(track).Error
	if !gorm.IsRecordNotFoundError(err) &&
		modTime.Before(track.UpdatedAt) {
		// we found the record but it hasn't changed
		return nil
	}
	tags, err := readTags(fullPath)
	if err != nil {
		return fmt.Errorf("when reading tags: %v", err)
	}
	trackNumber, totalTracks := tags.Track()
	discNumber, totalDiscs := tags.Disc()
	track.Path = fullPath
	track.Title = tags.Title()
	track.Artist = tags.Artist()
	track.DiscNumber = discNumber
	track.TotalDiscs = totalDiscs
	track.TotalTracks = totalTracks
	track.TrackNumber = trackNumber
	track.Year = tags.Year()
	track.Suffix = exten
	track.ContentType = mime
	track.Size = int(stat.Size())
	track.FolderID = s.currentDirStack.PeekID()
	//
	// set album artist basics
	albumArtist := &model.AlbumArtist{}
	err = s.tx.Where("name = ?", tags.AlbumArtist()).
		First(albumArtist).
		Error
	if gorm.IsRecordNotFoundError(err) {
		albumArtist.Name = tags.AlbumArtist()
		s.tx.Save(albumArtist)
	}
	track.AlbumArtistID = albumArtist.ID
	//
	// set temporary album's basics - will be updated with
	// cover after the tracks inserted when we exit the folder
	s.updateAlbum(fullPath, &model.Album{
		AlbumArtistID: albumArtist.ID,
		Title:         tags.Album(),
		Year:          tags.Year(),
	})
	//
	// update the track with our new album and finally save
	track.AlbumID = s.currentAlbum.ID
	s.tx.Save(track)
	return nil
}

func (s *Scanner) handleItem(fullPath string, info *godirwalk.Dirent) error {
	s.seenPaths[fullPath] = true
	stat, err := os.Stat(fullPath)
	if err != nil {
		return fmt.Errorf("error stating: %v", err)
	}
	if info.IsDir() {
		return s.handleFolder(fullPath, stat)
	}
	if isCover(fullPath) {
		return s.handleCover(fullPath, stat)
	}
	if mime, exten, ok := isAudio(fullPath); ok {
		return s.handleTrack(fullPath, stat, mime, exten)
	}
	return nil
}

func (s *Scanner) MigrateDB() error {
	defer logElapsed(time.Now(), "migrating database")
	s.db.Exec("PRAGMA foreign_keys = ON")
	s.tx = s.db.Begin()
	defer s.tx.Commit()
	s.tx.AutoMigrate(
		model.Album{},
		model.AlbumArtist{},
		model.Track{},
		model.Cover{},
		model.User{},
		model.Setting{},
		model.Play{},
		model.Folder{},
	)
	s.tx.FirstOrCreate(&model.User{}, model.User{
		Name:     "admin",
		Password: "admin",
		IsAdmin:  true,
	})
	return nil
}

func (s *Scanner) Start() error {
	if atomic.LoadInt32(&IsScanning) == 1 {
		return errors.New("already scanning")
	}
	atomic.StoreInt32(&IsScanning, 1)
	defer atomic.StoreInt32(&IsScanning, 0)
	defer logElapsed(time.Now(), "scanning")
	s.db.Exec("PRAGMA foreign_keys = ON")
	s.tx = s.db.Begin()
	defer s.tx.Commit()
	//
	// start scan logic
	err := godirwalk.Walk(s.musicPath, &godirwalk.Options{
		Callback:             s.handleItem,
		PostChildrenCallback: s.handleFolderCompletion,
		Unsorted:             true,
	})
	if err != nil {
		return errors.Wrap(err, "walking filesystem")
	}
	//
	// start cleaning logic
	log.Println("cleaning database")
	var tracks []*model.Track
	s.tx.Select("id, path").Find(&tracks)
	for _, track := range tracks {
		_, ok := s.seenPaths[track.Path]
		if ok {
			continue
		}
		s.tx.Delete(&track)
		log.Println("removed", track.Path)
	}
	// delete albums without tracks
	s.tx.Exec(`
        DELETE FROM albums
        WHERE  (SELECT count(id)
                FROM   tracks
                WHERE  album_id = albums.id) = 0;
       `)
	// delete artists without tracks
	s.tx.Exec(`
        DELETE FROM album_artists
        WHERE  (SELECT count(id)
                FROM   albums
                WHERE  album_artist_id = album_artists.id) = 0;
    `)
	return nil
}