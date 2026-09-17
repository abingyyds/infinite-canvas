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
	// 乐观锁版本号。客户端保存时回传它读到的版本，对不上就是有人先写过，拒绝覆盖。
	// 行插入时置 1；0 属于加列之前的历史行，当作"没人动过"处理。
	Revision int64 `json:"revision"`
}
