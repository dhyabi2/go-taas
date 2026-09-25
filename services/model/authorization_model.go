package model

import "time"

// Authorization is the GORM model of the model_authorizations table
// (feature-13, AD1) and the single source of truth for its schema
// (created by AutoMigrate, no hand-written DDL). It is one grant row:
// the organization may use the model. (The Go type drops the leading
// "model" for readability; the table and the proto message keep it.)
//
// One row means "this organization may use this model". The default-allow
// rule is derived from the rows, never stored: a model with zero grant
// rows is open to every organization, and the first row restricts it to
// the granted set (AD2).
//
// There are no foreign keys to models or organizations: a grant must
// survive a model deletion (DeleteModel cascades its own rows) and an
// organization being disabled (grants stay inert until it is enabled
// again), so the trailing-column constraints are plain indexes.
type Authorization struct {
	// ID is the server-generated UUID v4.
	ID string `gorm:"primaryKey;type:uuid"`
	// ModelID is the granted model's id; half of the composite unique
	// key that makes granting idempotent (AC2).
	ModelID string `gorm:"type:uuid;not null;uniqueIndex:idx_model_authorizations_model_org,priority:1;index:idx_model_auth_model_created,priority:1"`
	// OrganizationID is the granted organization; the other half of the
	// composite unique key.
	OrganizationID string `gorm:"size:64;not null;uniqueIndex:idx_model_authorizations_model_org,priority:2"`
	// GrantedBy is the caller's user id that granted access (AD8). It
	// records the person, not the organization they acted for.
	GrantedBy string `gorm:"size:64;not null"`
	// CreatedAt is the grant time (UTC); the list is ordered by it.
	CreatedAt time.Time `gorm:"index:idx_model_auth_model_created,priority:2,sort:DESC"`
}

// TableName returns the table name of Authorization.
func (Authorization) TableName() string { return "model_authorizations" }
