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

func GetIndexChat(ctx context.Context, chatID int64) (*IndexChat, error) {
	var IndexChat IndexChat
	if err := db.WithContext(ctx).Where("chat_id = ?", chatID).First(&IndexChat).Error; err != nil {
		return nil, err
	}
	return &IndexChat, nil
}

func DeleteIndexChat(ctx context.Context, chatID int64) error {
	if err := db.WithContext(ctx).Where("chat_id = ?", chatID).
		Delete(&IndexChat{ChatID: chatID}).Error; err != nil {
		// https://github.com/go-gorm/gorm/issues/5663
		watchedChatsID[chatID] = struct{}{} // rollback
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

func UpsertSubBot(ctx context.Context, subBot *SubBot) error {
	if err := db.WithContext(ctx).Save(subBot).Error; err != nil {
		return err
	}
	return nil
}

func GetAllSubBots(ctx context.Context) ([]*SubBot, error) {
	var subBots []*SubBot
	if err := db.WithContext(ctx).Find(&subBots).Error; err != nil {
		return nil, err
	}
	return subBots, nil
}

func GetSubBot(ctx context.Context, botID int64) (*SubBot, error) {
	var subBot SubBot
	if err := db.WithContext(ctx).Where("bot_id = ?", botID).First(&subBot).Error; err != nil {
		return nil, err
	}
	return &subBot, nil
}

func DeleteSubBot(ctx context.Context, botID int64) error {
	if err := db.WithContext(ctx).Where("bot_id = ?", botID).Delete(&SubBot{}).Error; err != nil {
		return err
	}
	return nil
}

func AddMemberToIndexChat(ctx context.Context, chatID int64, userInfo *UserInfo) error {
	var indexChat IndexChat
	if err := db.WithContext(ctx).Where("chat_id = ?", chatID).First(&indexChat).Error; err != nil {
		return err
	}

	var count int64
	if err := db.Table("index_chat_members").
		Where("index_chat_id = ? AND user_chat_id = ?", chatID, userInfo.ChatID).
		Count(&count).Error; err != nil {
		return err
	}

	if count > 0 {
		return nil
	}

	if err := db.WithContext(ctx).Model(&indexChat).Association("Members").Append(userInfo); err != nil {
		return err
	}

	return nil
}

func RemoveMemberFromIndexChat(ctx context.Context, chatID int64, userInfo *UserInfo) error {
	var indexChat IndexChat
	if err := db.WithContext(ctx).Where("chat_id = ?", chatID).First(&indexChat).Error; err != nil {
		return err
	}
	if err := db.WithContext(ctx).Model(&indexChat).Association("Members").Delete(userInfo); err != nil {
		return err
	}
	return nil
}

func IsMemberInIndexChat(ctx context.Context, chatID int64, userChatID int64) (bool, error) {
	var count int64
	err := db.WithContext(ctx).Table("index_chat_members").
		Where("index_chat_id = ? AND user_chat_id = ?", chatID, userChatID).
		Count(&count).Error
	if err != nil {
		return false, err
	}
	return count > 0, nil
}
