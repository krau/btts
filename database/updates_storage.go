package database

import (
	"context"
	"errors"

	"github.com/gotd/td/telegram/updates"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// UpdatesStorage persists account-wide recovery cursors independently of indexed chats.
type UpdatesStorage struct{}

var _ updates.StateStorage = (*UpdatesStorage)(nil)

// NewUpdatesStorage returns the storage used for ClientOpts.UpdateStateStorage.
func NewUpdatesStorage() *UpdatesStorage {
	return &UpdatesStorage{}
}

// Legacy cursors were not account-scoped; only the authenticated account may adopt them.
const legacyUpdatesStateID = 1

func (s *UpdatesStorage) GetState(ctx context.Context, userID int64) (updates.State, bool, error) {
	var row UpdatesState
	var found bool
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := migrateLegacyUpdates(tx, userID); err != nil {
			return err
		}
		var err error
		row, found, err = updatesRowByID(tx, uint(userID))
		return err
	})
	if err != nil {
		return updates.State{}, false, err
	}
	return row.asState(), found && row.meaningful(), nil
}

func updatesRowByID(tx *gorm.DB, id uint) (UpdatesState, bool, error) {
	var row UpdatesState
	err := tx.Where("id = ?", id).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return UpdatesState{}, false, nil
	}
	return row, err == nil, err
}

func migrateLegacyUpdates(tx *gorm.DB, userID int64) error {
	if uint(userID) != legacyUpdatesStateID {
		legacy, found, err := updatesRowByID(tx, legacyUpdatesStateID)
		if err != nil {
			return err
		}
		if found {
			current, currentFound, err := updatesRowByID(tx, uint(userID))
			if err != nil {
				return err
			}
			if !currentFound || !current.meaningful() {
				legacy.ID = uint(userID)
				if err := tx.Save(&legacy).Error; err != nil {
					return err
				}
			}
			if err := tx.Delete(&UpdatesState{}, legacyUpdatesStateID).Error; err != nil {
				return err
			}
		}
	}
	// Keep the old SQLite column, but consume it once so later accounts cannot adopt it.
	if tx.Migrator().HasColumn(&IndexChat{}, "pts") {
		if err := tx.Exec(`INSERT INTO updates_channel_states (user_id, channel_id, pts)
			SELECT ?, chat_id, pts FROM index_chats WHERE pts > 0
			ON CONFLICT (user_id, channel_id) DO NOTHING`, userID).Error; err != nil {
			return err
		}
		if err := tx.Table("index_chats").Where("pts <> 0").Update("pts", 0).Error; err != nil {
			return err
		}
	}
	return nil
}

func (r *UpdatesState) asState() updates.State {
	return updates.State{Pts: r.Pts, Qts: r.Qts, Date: r.Date, Seq: r.Seq}
}

// Legacy installations could contain a placeholder all-zero account state.
func (r *UpdatesState) meaningful() bool {
	return r.Pts != 0 || r.Qts != 0 || r.Date != 0 || r.Seq != 0
}

func (s *UpdatesStorage) SetState(ctx context.Context, userID int64, state updates.State) error {
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := migrateLegacyUpdates(tx, userID); err != nil {
			return err
		}
		return tx.Save(&UpdatesState{
			ID:   uint(userID),
			Pts:  state.Pts,
			Qts:  state.Qts,
			Date: state.Date,
			Seq:  state.Seq,
		}).Error
	})
}

// Incremental setters must reject missing account state under the gotd contract.
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

func (s *UpdatesStorage) SetPts(ctx context.Context, userID int64, pts int) error {
	return s.setStateColumns(ctx, userID, map[string]any{"pts": pts})
}

func (s *UpdatesStorage) SetQts(ctx context.Context, userID int64, qts int) error {
	return s.setStateColumns(ctx, userID, map[string]any{"qts": qts})
}

func (s *UpdatesStorage) SetDate(ctx context.Context, userID int64, date int) error {
	return s.setStateColumns(ctx, userID, map[string]any{"date": date})
}

func (s *UpdatesStorage) SetSeq(ctx context.Context, userID int64, seq int) error {
	return s.setStateColumns(ctx, userID, map[string]any{"seq": seq})
}

func (s *UpdatesStorage) SetDateSeq(ctx context.Context, userID int64, date, seq int) error {
	return s.setStateColumns(ctx, userID, map[string]any{"date": date, "seq": seq})
}

func (s *UpdatesStorage) GetChannelPts(ctx context.Context, userID, channelID int64) (int, bool, error) {
	var row updatesChannelState
	err := db.WithContext(ctx).Where("user_id = ? AND channel_id = ?", userID, channelID).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return row.Pts, true, nil
}

func (s *UpdatesStorage) SetChannelPts(ctx context.Context, userID, channelID int64, pts int) error {
	return db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "user_id"}, {Name: "channel_id"}},
		DoUpdates: clause.AssignmentColumns([]string{"pts"}),
	}).Create(&updatesChannelState{UserID: userID, ChannelID: channelID, Pts: pts}).Error
}

func (s *UpdatesStorage) ForEachChannels(ctx context.Context, userID int64, f func(ctx context.Context, channelID int64, pts int) error) error {
	var channels []updatesChannelState
	if err := db.WithContext(ctx).Where("user_id = ?", userID).Find(&channels).Error; err != nil {
		return err
	}
	for _, channel := range channels {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := f(ctx, channel.ChannelID, channel.Pts); err != nil {
			return err
		}
	}
	return nil
}
