package tenancy

import (
	"context"

	"gorm.io/gorm"
)

// MemberOrg is one (org, role) membership of a user — the session
// derivation input (feature #10, AD2/AD11).
type MemberOrg struct {
	OrganizationID string
	Role           string
}

// MembershipResolver is the read interface the auth module consumes to
// derive a session's accessible orgs and per-org roles from org_members
// (authoritative over IdP claims, AD2). It is injected into the auth
// service at wiring time (the SetOrgGuard pattern).
type MembershipResolver struct {
	repo *MembershipRepository
}

// NewMembershipResolver constructs a MembershipResolver bound to a GORM
// database.
func NewMembershipResolver(db *gorm.DB) *MembershipResolver {
	return &MembershipResolver{repo: NewMembershipRepository(db)}
}

// AccessibleOrgsAndRoles returns the user's (org, role) memberships.
func (r *MembershipResolver) AccessibleOrgsAndRoles(ctx context.Context, userID string) ([]MemberOrg, error) {
	rows, err := r.repo.ListMemberOrgs(ctx, userID)
	if err != nil {
		return nil, err
	}
	out := make([]MemberOrg, 0, len(rows))
	for _, row := range rows {
		out = append(out, MemberOrg{OrganizationID: row.OrganizationID, Role: row.Role})
	}
	return out, nil
}
