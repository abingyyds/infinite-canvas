package model

type GatewayProvider string

const (
	GatewayProviderMain GatewayProvider = "main"
	GatewayProviderSite GatewayProvider = "site"
)

// GatewayAccount stores a user-scoped upstream gateway credential.
type GatewayAccount struct {
	ID             string          `json:"id" gorm:"primaryKey"`
	UserID         string          `json:"userId" gorm:"index"`
	Provider       GatewayProvider `json:"provider" gorm:"index"`
	BaseURL        string          `json:"baseUrl"`
	ExternalUserID string          `json:"externalUserId" gorm:"index"`
	Username       string          `json:"username"`
	Email          string          `json:"email"`
	DisplayName    string          `json:"displayName"`
	DistributorID  string          `json:"distributorId"`
	DistributorSlug string         `json:"distributorSlug"`
	SiteHost       string          `json:"siteHost"`
	SessionCookie  string          `json:"sessionCookie,omitempty" gorm:"type:text"`
	APIKey         string          `json:"apiKey,omitempty" gorm:"type:text"`
	APIKeyID       string          `json:"apiKeyId"`
	Models         string          `json:"models" gorm:"type:text"`
	ModelsSource   string          `json:"modelsSource"`
	CreatedAt      string          `json:"createdAt"`
	UpdatedAt      string          `json:"updatedAt"`
}
