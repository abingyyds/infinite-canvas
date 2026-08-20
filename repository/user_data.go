package repository

import (
	"github.com/basketikun/infinite-canvas/model"
)

func GetUserDataSnapshot(userID string, domain string) (model.UserDataSnapshot, bool, error) {
	db, err := DB()
	if err != nil {
		return model.UserDataSnapshot{}, false, err
	}
	// 用 Find 而不是 First：画布每次读写都要查一次有没有遗留的整库快照，
	// First 会把"没有记录"当错误打进日志，稳定状态下那是每个请求一行噪音。
	items := []model.UserDataSnapshot{}
	if err := db.Where("user_id = ? AND domain = ?", userID, domain).Limit(1).Find(&items).Error; err != nil {
		return model.UserDataSnapshot{}, false, err
	}
	if len(items) == 0 {
		return model.UserDataSnapshot{}, false, nil
	}
	return items[0], true, nil
}

func SaveUserDataSnapshot(item model.UserDataSnapshot) (model.UserDataSnapshot, error) {
	db, err := DB()
	if err != nil {
		return item, err
	}
	return item, db.Save(&item).Error
}
