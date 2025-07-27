package utils

import (
	"errors"
	"fmt"

	"github.com/celestix/gotgproto/functions"
	"github.com/gabriel-vasile/mimetype"
	"github.com/gotd/td/telegram/downloader"
	"github.com/gotd/td/tg"
)

type TGFile interface {
	Location() tg.InputFileLocationClass
	Client() downloader.Client // witch client to use for downloading
	Size() int64
	Name() string
}

type tgFile struct {
	location tg.InputFileLocationClass
	size     int64
	name     string
	client   downloader.Client
}

func (f *tgFile) Location() tg.InputFileLocationClass {
	return f.location
}

func (f *tgFile) Size() int64 {
	return f.size
}

func (f *tgFile) Name() string {
	return f.name
}

func (f *tgFile) Client() downloader.Client {
	return f.client
}

func NewTGFile(
	location tg.InputFileLocationClass,
	client downloader.Client,
	size int64,
	name string,
) TGFile {
	f := &tgFile{
		location: location,
		client:   client,
		size:     size,
		name:     name,
	}
	return f
}

func FileFromMedia(media tg.MessageMediaClass, client downloader.Client) (TGFile, error) {
	switch m := media.(type) {
	case *tg.MessageMediaDocument:
		document, ok := m.Document.AsNotEmpty()
		if !ok {
			return nil, errors.New("document is empty")
		}
		fileName := ""
		for _, attribute := range document.Attributes {
			if name, ok := attribute.(*tg.DocumentAttributeFilename); ok {
				fileName = name.GetFileName()
				break
			}
		}
		if fileName == "" {
			mmt := mimetype.Lookup(document.GetMimeType())
			if mmt != nil {
				fileName = fmt.Sprintf("%d.%s", document.GetID(), mmt.Extension())
			}
		}
		file := NewTGFile(
			document.AsInputDocumentFileLocation(),
			client,
			document.Size,
			fileName,
		)
		return file, nil
	case *tg.MessageMediaPhoto:
		photo, ok := m.Photo.AsNotEmpty()
		if !ok {
			return nil, errors.New("photo is empty")
		}
		sizes := photo.Sizes
		if len(sizes) == 0 {
			return nil, errors.New("photo sizes are empty")
		}
		photoSize := sizes[len(sizes)-1]
		size, ok := photoSize.AsNotEmpty()
		if !ok {
			return nil, errors.New("photo size is empty")
		}
		location := new(tg.InputPhotoFileLocation)
		location.ID = photo.GetID()
		location.AccessHash = photo.GetAccessHash()
		location.FileReference = photo.GetFileReference()
		location.ThumbSize = size.GetType()
		fileName, err := functions.GetMediaFileName(m)
		if err != nil {
			fileName = fmt.Sprintf("photo_%d.png", photo.GetID())
		}
		file := NewTGFile(
			location,
			client,
			0, // Photo size is not available in InputPhotoFileLocation
			fileName,
		)
		return file, nil
	}
	return nil, fmt.Errorf("unsupported media type: %T", media)
}
