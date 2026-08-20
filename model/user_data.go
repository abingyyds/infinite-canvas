package model

// UserDataSnapshot stores a user-scoped JSON snapshot for a frontend data domain.
type UserDataSnapshot struct {
	UserID    string `json:"userId" gorm:"primaryKey"`
	Domain    string `json:"domain" gorm:"primaryKey"`
	Data      string `json:"data" gorm:"type:text"`
	UpdatedAt string `json:"updatedAt"`
}

// UserCanvasProject stores one canvas project per row. The whole library used to live in a
// single UserDataSnapshot row, so every save rewrote all of it and a large library eventually
// exceeded the snapshot cap and could not be saved at all.
type UserCanvasProject struct {
	UserID    string `json:"userId" gorm:"primaryKey"`
	ProjectID string `json:"projectId" gorm:"primaryKey"`
	// 画布在列表里的显示顺序，由客户端的 keepIds 决定
	SortIndex int    `json:"sortIndex"`
	Data      string `json:"data" gorm:"type:text"`
	UpdatedAt string `json:"updatedAt"`
}
