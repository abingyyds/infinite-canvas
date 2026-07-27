package repository

import (
	"errors"

	"github.com/basketikun/infinite-canvas/model"
	"gorm.io/gorm"
)

func GetUserDataSnapshot(userID string, domain string) (model.UserDataSnapshot, bool, error) {
	db, err := DB()
	if err != nil {
		return model.UserDataSnapshot{}, false, err
	}
	item := model.UserDataSnapshot{}
	err = db.Where("user_id = ? AND domain = ?", userID, domain).First(&item).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return model.UserDataSnapshot{}, false, nil
	}
	return item, err == nil, err
}

func SaveUserDataSnapshot(item model.UserDataSnapshot) (model.UserDataSnapshot, error) {
	db, err := DB()
	if err != nil {
		return item, err
	}
	return item, db.Save(&item).Error
}
