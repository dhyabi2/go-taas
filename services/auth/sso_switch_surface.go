// Package auth implements the authentication and authorization service:
// local accounts, API keys, SSO federation, and the gateway-facing key
// verification used to authenticate inference traffic.
//
// Unified login & role-based routing (feature-22): SwitchSurface and
// SwitchSurfaceAdmin mint a fresh session for the target realm for the
// current user, so an administrator can move between the user and admin
// consoles without signing in again.
package auth

import (
	"context"
	"time"

	"github.com/google/uuid"

	authv1 "github.com/go-taas/go-taas/proto/taas/auth/v1"

	apierrors "github.com/go-taas/go-taas/pkg/errors"
)

// SwitchSurface mints an admin-realm session for the current user
// (feature-22 AD4/AD5). It is reached on the user prefix
// (/api/v1/auth/session:switch-to-admin) with a user-realm session; the
// gateway realm guard enforces the realm. A caller without an admin
// role receives 10036. The source session is left intact so the user
// can switch back (AD9).
func (s *Service) SwitchSurface(ctx context.Context, _ *authv1.SwitchSurfaceRequest) (*authv1.SwitchSurfaceResponse, error) {
	return s.mintSwitchSession(ctx, RealmAdmin, true)
}

// SwitchSurfaceAdmin mints a user-realm session for the current user
// (feature-22 AD4/AD5). It is reached on the admin prefix
// (/api/v1/admin/auth/session:switch-to-user) with an admin-realm
// session; the gateway realm guard enforces the realm. No role check is
// needed — an admin is always allowed to use the user console. The
// source session is left intact (AD9).
func (s *Service) SwitchSurfaceAdmin(ctx context.Context, _ *authv1.SwitchSurfaceRequest) (*authv1.SwitchSurfaceResponse, error) {
	return s.mintSwitchSession(ctx, RealmUser, false)
}

// mintSwitchSession is the shared switch body. It resolves the current
// session, optionally validates the admin role, and mints a fresh
// session with the same identity but the target realm (AD9).
func (s *Service) mintSwitchSession(ctx context.Context, targetRealm string, requireAdmin bool) (*authv1.SwitchSurfaceResponse, error) {
	sess, err := s.sessionFromContext(ctx)
	if err != nil {
		return nil, err
	}
	if requireAdmin && !s.isAdminRole(sess.Roles) {
		return nil, apierrors.New(apierrors.CodeForbidden)
	}
	if s.sessionStore == nil {
		return nil, apierrors.New(apierrors.CodeSessionInvalid)
	}
	sessionID := uuid.NewString()
	accessToken := newAccessToken()
	now := time.Now()
	expiresAt := now.Add(s.sessionTTL()).Unix()
	target := &Session{
		SessionID:      sessionID,
		UserID:         sess.UserID,
		Username:       sess.Username,
		Email:          sess.Email,
		Roles:          sess.Roles,
		AccessibleOrgs: sess.AccessibleOrgs,
		ActiveOrg:      sess.ActiveOrg,
		ExpiresAt:      expiresAt,
		CreatedAt:      now.Unix(),
		Realm:          targetRealm,
	}
	if err := s.sessionStore.Create(ctx, target, accessToken); err != nil {
		return nil, err
	}
	return &authv1.SwitchSurfaceResponse{
		Response:     okResponse(),
		SessionToken: sessionID,
		Realm:        targetRealm,
		ExpiresAt:    expiresAt,
	}, nil
}