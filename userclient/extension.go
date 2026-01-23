package userclient

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"github.com/charmbracelet/log"
	"github.com/gotd/td/telegram/uploader"
	"github.com/gotd/td/tg"
	"github.com/krau/mygotg/ext"
	"github.com/rs/xid"
	"go.uber.org/multierr"
)

type ExtenApiFunc func(ctx context.Context, ectx *ext.Context, input map[string]any) (map[string]any, error)

var ExtenApis = map[string]ExtenApiFunc{
	"SetProfile": ExtenSetProfile,
}

func (u *UserClient) CallExtenApi(ctx context.Context, name string, input map[string]any) (map[string]any, error) {
	if u.ectx == nil {
		u.ectx = u.TClient.CreateContext()
	}
	if fn, ok := ExtenApis[name]; ok {
		return fn(ctx, u.ectx, input)
	}
	return nil, errors.New("unknown extension API: " + name)
}

// ExtenSetProfile updates the user's profile information.
//
// Input:
//
// - first_name: First name of the user.
//
// - last_name: Last name of the user.
//
// - avatar_url: URL of the avatar image (can be base64 encoded).
//
// - bio: Bio of the user.
var ExtenSetProfile = func(ctx context.Context, ectx *ext.Context, input map[string]any) (map[string]any, error) {
	firstName, _ := input["first_name"].(string)
	lastName, _ := input["last_name"].(string)
	avatarUrl, _ := input["avatar_url"].(string)
	bio, _ := input["bio"].(string)
	username, _ := input["username"].(string)
	if firstName == "" && lastName == "" && avatarUrl == "" && bio == "" && username == "" {
		return nil, errors.New("no profile data provided")
	}
	raw := ectx.Raw
	var errs []error
	if firstName != "" || lastName != "" {
		req := &tg.AccountUpdateProfileRequest{}
		req.SetFirstName(firstName)
		req.SetLastName(lastName)
		if bio != "" {
			req.SetAbout(bio)
		}
		_, err := raw.AccountUpdateProfile(ctx, req)
		if err != nil {
			errs = append(errs, fmt.Errorf("failed to update profile: %w", err))
		}
	} else if bio != "" {
		req := &tg.AccountUpdateProfileRequest{}
		req.SetAbout(bio)
		_, err := raw.AccountUpdateProfile(ctx, req)
		if err != nil {
			errs = append(errs, fmt.Errorf("failed to update bio: %w", err))
		}
	}
	if avatarUrl != "" {
		err := func() error {
			deleteOldErr := func() error {
				req := &tg.PhotosGetUserPhotosRequest{
					UserID: ectx.Self.AsInput(),
					Limit:  1,
					Offset: 1,
				}
				photos, err := raw.PhotosGetUserPhotos(ctx, req)
				if err != nil {
					return fmt.Errorf("failed to get user photos: %w", err)
				}
				if len(photos.GetPhotos()) == 0 {
					return nil // no photos to delete
				}
				photo := photos.GetPhotos()[0]
				if photo == nil {
					return nil // no photo to delete
				}
				photoInput, ok := photo.AsNotEmpty()
				if !ok {
					return fmt.Errorf("unexpected empty photo in response")
				}
				_, deleteOldErr := raw.PhotosDeletePhotos(ctx, []tg.InputPhotoClass{photoInput.AsInput()})
				if deleteOldErr != nil {
					return fmt.Errorf("failed to delete old profile photo: %w", deleteOldErr)
				}
				return nil
			}()
			if deleteOldErr != nil {
				log.FromContext(ctx).Warn("failed to delete old profile photo", "error", deleteOldErr)
			}
			var file tg.InputFileClass
			var err error
			if strings.HasPrefix(avatarUrl, "http") {
				file, err = uploader.NewUploader(raw).FromURL(ctx, avatarUrl)
			} else {
				// base64
				base64Data := avatarUrl
				if idx := strings.Index(base64Data, ","); idx != -1 {
					base64Data = base64Data[idx+1:]
				}
				data, err2 := base64.StdEncoding.DecodeString(base64Data)
				if err2 != nil {
					return fmt.Errorf("failed to decode base64 avatar: %w", err)
				}
				file, err = uploader.NewUploader(raw).FromBytes(ctx, fmt.Sprintf("%s.jpg", xid.New().String()), data)
			}
			if err != nil {
				return fmt.Errorf("failed to upload avatar: %w", err)
			}
			req := &tg.PhotosUploadProfilePhotoRequest{}
			req.SetFile(file)
			photosPhoto, err := raw.PhotosUploadProfilePhoto(ctx, req)
			if err != nil {
				return fmt.Errorf("failed to set profile photo: %w", err)
			}
			photo, ok := photosPhoto.GetPhotoAsNotEmpty()
			if !ok {
				return fmt.Errorf("unexpected empty photo in response")
			}
			_, err = raw.PhotosUpdateProfilePhoto(ctx, &tg.PhotosUpdateProfilePhotoRequest{
				ID: photo.AsInput(),
			})
			if err != nil {
				return fmt.Errorf("failed to update profile photo: %w", err)
			}
			return nil
		}()
		if err != nil {
			errs = append(errs, fmt.Errorf("failed to update avatar: %w", err))
		}
	}
	if username != "" {
		_, err := raw.AccountUpdateUsername(ctx, username)
		if err != nil {
			errs = append(errs, fmt.Errorf("failed to update username: %w", err))
		}
	}
	if len(errs) > 0 {
		return nil, multierr.Combine(errs...)
	}
	return map[string]any{
		"message": "Profile updated successfully",
	}, nil
}
