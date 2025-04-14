package database

import "context"

func UpsertUserInfo(ctx context.Context, userInfo *UserInfo) error {
	if err := db.WithContext(ctx).Save(userInfo).Error; err != nil {
		return err
	}
	return nil
}

func GetUserInfo(ctx context.Context, chatID int64) (*UserInfo, error) {
	var userInfo UserInfo
	if err := db.WithContext(ctx).Where("chat_id = ?", chatID).First(&userInfo).Error; err != nil {
		return nil, err
	}
	return &userInfo, nil
}

func UpsertIndexChat(ctx context.Context, IndexChat *IndexChat) error {
	if err := db.WithContext(ctx).Save(IndexChat).Error; err != nil {
		return err
	}
	return nil
}

func UnwatchIndexChat(ctx context.Context, chatID int64) error {
	if err := db.WithContext(ctx).Model(&IndexChat{}).Where("chat_id = ?", chatID).Update("watching", false).Error; err != nil {
		return err
	}
	return nil
}

func WatchIndexChat(ctx context.Context, chatID int64) error {
	if err := db.WithContext(ctx).Model(&IndexChat{}).Where("chat_id = ?", chatID).Update("watching", true).Error; err != nil {
		return err
	}
	return nil
}

func GetIndexChat(ctx context.Context, chatID int64) (*IndexChat, error) {
	var IndexChat IndexChat
	if err := db.WithContext(ctx).Where("chat_id = ?", chatID).First(&IndexChat).Error; err != nil {
		return nil, err
	}
	return &IndexChat, nil
}

func DeleteIndexChat(ctx context.Context, chatID int64) error {
	delete(WatchedChatsID, chatID)
	if err := db.WithContext(ctx).Where("chat_id = ?", chatID).Delete(&IndexChat{}).Error; err != nil {
		WatchedChatsID[chatID] = struct{}{} // rollback
		return err
	}
	return nil
}

func GetAllIndexChats(ctx context.Context) ([]*IndexChat, error) {
	var IndexChats []*IndexChat
	if err := db.WithContext(ctx).Find(&IndexChats).Error; err != nil {
		return nil, err
	}
	return IndexChats, nil
}

func GetAllPublicIndexChats(ctx context.Context) ([]*IndexChat, error) {
	var IndexChats []*IndexChat
	if err := db.WithContext(ctx).Where("public = ?", true).Find(&IndexChats).Error; err != nil {
		return nil, err
	}
	return IndexChats, nil
}

func UpdateIndexChatPublic(ctx context.Context, chatID int64, public bool) error {
	if err := db.WithContext(ctx).Model(&IndexChat{}).Where("chat_id = ?", chatID).Update("public", public).Error; err != nil {
		return err
	}
	return nil
}
