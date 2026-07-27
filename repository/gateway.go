package repository

import (
	"errors"

	"github.com/basketikun/infinite-canvas/model"
	"gorm.io/gorm"
)

func GetGatewayAccount(userID string, provider model.GatewayProvider, baseURL string) (model.GatewayAccount, bool, error) {
	db, err := DB()
	if err != nil {
		return model.GatewayAccount{}, false, err
	}
	return findGatewayAccount(db, "user_id = ? AND provider = ? AND base_url = ?", userID, provider, baseURL)
}

func GetGatewayAccountByExternal(provider model.GatewayProvider, baseURL string, externalUserID string) (model.GatewayAccount, bool, error) {
	db, err := DB()
	if err != nil {
		return model.GatewayAccount{}, false, err
	}
	return findGatewayAccount(db, "provider = ? AND base_url = ? AND external_user_id = ?", provider, baseURL, externalUserID)
}

func FirstGatewayAccountByUser(userID string) (model.GatewayAccount, bool, error) {
	db, err := DB()
	if err != nil {
		return model.GatewayAccount{}, false, err
	}
	return findGatewayAccount(db, "user_id = ?", userID)
}

func SaveGatewayAccount(account model.GatewayAccount) (model.GatewayAccount, error) {
	db, err := DB()
	if err != nil {
		return account, err
	}
	return account, db.Save(&account).Error
}

func findGatewayAccount(db *gorm.DB, query string, args ...any) (model.GatewayAccount, bool, error) {
	account := model.GatewayAccount{}
	err := db.Where(query, args...).Order("updated_at desc").First(&account).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return model.GatewayAccount{}, false, nil
	}
	return account, err == nil, err
}
