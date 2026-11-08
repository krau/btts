package database

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
)

var (
	// ErrImportSuperseded means the import generation changed while a worker
	// was still running. Callers must not persist progress for the old run.
	ErrImportSuperseded   = errors.New("import superseded")
	ErrImportInProgress   = errors.New("import already in progress")
	ErrImportCompleted    = errors.New("import already completed")
	ErrImportModeMismatch = errors.New("import was started with a different mode")
)

// StartImport atomically claims an import generation for a chat. resumed
// reports whether previous progress is being continued.
func StartImport(ctx context.Context, chatID int64, mode string, lease time.Duration) (imp *IndexImport, resumed bool, err error) {
	now := time.Now().Unix()
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var existing IndexImport
		findErr := tx.Where("chat_id = ?", chatID).First(&existing).Error
		switch {
		case errors.Is(findErr, gorm.ErrRecordNotFound):
			var chats int64
			if err := tx.Model(&IndexChat{}).Where("chat_id = ?", chatID).Count(&chats).Error; err != nil {
				return err
			}
			if chats > 0 {
				// Chats indexed before resumable imports have no import row.
				return ErrImportCompleted
			}
			imp = &IndexImport{
				ChatID:     chatID,
				Generation: time.Now().UnixNano(),
				Mode:       mode,
				LeaseAt:    now,
			}
			return tx.Create(imp).Error
		case findErr != nil:
			return findErr
		}
		if existing.Completed {
			return ErrImportCompleted
		}
		if lease > 0 && now-existing.LeaseAt < int64(lease/time.Second) {
			return ErrImportInProgress
		}
		if existing.Mode != "" && existing.Mode != mode {
			return ErrImportModeMismatch
		}
		generation := time.Now().UnixNano()
		if generation <= existing.Generation {
			generation = existing.Generation + 1
		}
		existing.Generation = generation
		existing.Mode = mode
		existing.LeaseAt = now
		imp = &existing
		resumed = true
		return tx.Model(&IndexImport{}).Where("chat_id = ?", chatID).Updates(map[string]any{
			"generation": generation,
			"mode":       mode,
			"lease_at":   now,
		}).Error
	})
	if err != nil {
		return nil, false, err
	}
	return imp, resumed, nil
}

// GetImport returns the import state of a chat.
func GetImport(ctx context.Context, chatID int64) (*IndexImport, error) {
	var imp IndexImport
	if err := db.WithContext(ctx).Where("chat_id = ?", chatID).First(&imp).Error; err != nil {
		return nil, err
	}
	return &imp, nil
}

// UpdateImportProgress persists the cursor of one generation and renews its lease.
func UpdateImportProgress(ctx context.Context, chatID, generation int64, importRange, cursor int, completed bool) error {
	updates := map[string]any{
		"range_index": importRange,
		"cursor":      cursor,
		"lease_at":    time.Now().Unix(),
	}
	if completed {
		updates["completed"] = true
	}
	return updateImportGeneration(ctx, chatID, generation, updates)
}

// RefreshImport renews the lease of a running generation.
func RefreshImport(ctx context.Context, chatID, generation int64) error {
	return updateImportGeneration(ctx, chatID, generation, map[string]any{
		"lease_at": time.Now().Unix(),
	})
}

// StopImport releases the lease so a manual retry can resume immediately.
func StopImport(ctx context.Context, chatID, generation int64) error {
	return updateImportGeneration(ctx, chatID, generation, map[string]any{
		"lease_at": 0,
	})
}

func updateImportGeneration(ctx context.Context, chatID, generation int64, updates map[string]any) error {
	res := db.WithContext(ctx).Model(&IndexImport{}).
		Where("chat_id = ? AND generation = ?", chatID, generation).
		Updates(updates)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrImportSuperseded
	}
	return nil
}

// ResetIncompleteImports clears leases left behind by a previous process so a
// restart can resume immediately. It must only run in the serving process.
func ResetIncompleteImports(ctx context.Context) error {
	return db.WithContext(ctx).Model(&IndexImport{}).
		Where("completed = ?", false).
		Update("lease_at", 0).Error
}
