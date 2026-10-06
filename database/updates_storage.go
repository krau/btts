package database

import (
	"context"
	"errors"

	"github.com/gotd/td/telegram/updates"
	"gorm.io/gorm"
)

// UpdatesStorage persists gotd's update-manager state (pts/qts/seq and the
// per-channel pts) in the bot database, so updates missed while the process was
// offline are recovered with updates.getDifference on the next start.
//
// It replaces the hand-written catch-up that used to live in userclient: the
// manager owns the sequence numbers now, and it uses this storage only to keep
// them across restarts.
//
// The account state reuses the UpdatesState table (one row per account, keyed by
// the Telegram user ID) and the channel pts reuse IndexChat.Pts, so the manager
// only tracks chatter the bot already knows about; the bot's chat lists are not
// touched.
type UpdatesStorage struct{}

var _ updates.StateStorage = (*UpdatesStorage)(nil)

// NewUpdatesStorage returns the storage used for ClientOpts.UpdateStateStorage.
func NewUpdatesStorage() *UpdatesStorage {
	return &UpdatesStorage{}
}

// legacyUpdatesStateID is where the hand-rolled catch-up stored the account
// state before the update manager took over.
const legacyUpdatesStateID = 1

// GetState implements updates.StateStorage.
func (s *UpdatesStorage) GetState(ctx context.Context, userID int64) (updates.State, bool, error) {
	row, found, err := s.rowByID(ctx, uint(userID))
	if err != nil {
		return updates.State{}, false, err
	}
	if found {
		return row.asState(), row.meaningful(), nil
	}
	// Adopt the row the previous implementation wrote (fixed ID 1) so an
	// upgraded installation resumes from its old cursor instead of treating the
	// account as new and skipping everything missed while it was down.
	if uint(userID) == legacyUpdatesStateID {
		return updates.State{}, false, nil
	}
	legacy, found, err := s.rowByID(ctx, legacyUpdatesStateID)
	if err != nil || !found {
		return updates.State{}, false, err
	}
	if err := db.WithContext(ctx).Model(&UpdatesState{}).
		Where("id = ?", legacyUpdatesStateID).Update("id", uint(userID)).Error; err != nil {
		return updates.State{}, false, err
	}
	return legacy.asState(), legacy.meaningful(), nil
}

func (s *UpdatesStorage) rowByID(ctx context.Context, id uint) (*UpdatesState, bool, error) {
	var row UpdatesState
	err := db.WithContext(ctx).Where("id = ?", id).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return &row, true, nil
}

func (r *UpdatesState) asState() updates.State {
	return updates.State{Pts: r.Pts, Qts: r.Qts, Date: r.Date, Seq: r.Seq}
}

// meaningful reports whether the row holds a cursor worth resuming from. The
// old GetUpdatesState created an all-zero row on first access.
func (r *UpdatesState) meaningful() bool {
	return r.Pts != 0 || r.Qts != 0 || r.Date != 0 || r.Seq != 0
}

// SetState implements updates.StateStorage.
func (s *UpdatesStorage) SetState(ctx context.Context, userID int64, state updates.State) error {
	return db.WithContext(ctx).Save(&UpdatesState{
		ID:   uint(userID),
		Pts:  state.Pts,
		Qts:  state.Qts,
		Date: state.Date,
		Seq:  state.Seq,
	}).Error
}

// setStateColumns updates individual fields of an existing account state.
// Per the gotd contract, the incremental setters report an error when no state
// has been stored yet, so the manager falls back to a full resynchronization.
func (s *UpdatesStorage) setStateColumns(ctx context.Context, userID int64, values map[string]any) error {
	res := db.WithContext(ctx).Model(&UpdatesState{}).Where("id = ?", uint(userID)).Updates(values)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

// SetPts implements updates.StateStorage.
func (s *UpdatesStorage) SetPts(ctx context.Context, userID int64, pts int) error {
	return s.setStateColumns(ctx, userID, map[string]any{"pts": pts})
}

// SetQts implements updates.StateStorage.
func (s *UpdatesStorage) SetQts(ctx context.Context, userID int64, qts int) error {
	return s.setStateColumns(ctx, userID, map[string]any{"qts": qts})
}

// SetDate implements updates.StateStorage.
func (s *UpdatesStorage) SetDate(ctx context.Context, userID int64, date int) error {
	return s.setStateColumns(ctx, userID, map[string]any{"date": date})
}

// SetSeq implements updates.StateStorage.
func (s *UpdatesStorage) SetSeq(ctx context.Context, userID int64, seq int) error {
	return s.setStateColumns(ctx, userID, map[string]any{"seq": seq})
}

// SetDateSeq implements updates.StateStorage.
func (s *UpdatesStorage) SetDateSeq(ctx context.Context, userID int64, date, seq int) error {
	return s.setStateColumns(ctx, userID, map[string]any{"date": date, "seq": seq})
}

// GetChannelPts implements updates.StateStorage.
func (s *UpdatesStorage) GetChannelPts(ctx context.Context, userID, channelID int64) (int, bool, error) {
	var chat IndexChat
	err := db.WithContext(ctx).Select("chat_id", "pts").Where("chat_id = ?", channelID).First(&chat).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	if chat.Pts <= 0 {
		// No sequence recorded yet: report unknown rather than a bogus 0.
		return 0, false, nil
	}
	return chat.Pts, true, nil
}

// SetChannelPts implements updates.StateStorage.
//
// Chats the bot does not know about are skipped: creating IndexChat rows for
// unrelated channels would leak them into the bot's chat lists.
func (s *UpdatesStorage) SetChannelPts(ctx context.Context, userID, channelID int64, pts int) error {
	return db.WithContext(ctx).Model(&IndexChat{}).Where("chat_id = ?", channelID).Update("pts", pts).Error
}

// ForEachChannels implements updates.StateStorage.
func (s *UpdatesStorage) ForEachChannels(ctx context.Context, userID int64, f func(ctx context.Context, channelID int64, pts int) error) error {
	var chats []IndexChat
	if err := db.WithContext(ctx).Select("chat_id", "pts").Where("pts > 0").Find(&chats).Error; err != nil {
		return err
	}
	for _, chat := range chats {
		if err := f(ctx, chat.ChatID, chat.Pts); err != nil {
			return err
		}
	}
	return nil
}
