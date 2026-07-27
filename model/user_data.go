package model

// UserDataSnapshot stores a user-scoped JSON snapshot for a frontend data domain.
type UserDataSnapshot struct {
	UserID    string `json:"userId" gorm:"primaryKey"`
	Domain    string `json:"domain" gorm:"primaryKey"`
	Data      string `json:"data" gorm:"type:text"`
	UpdatedAt string `json:"updatedAt"`
}
