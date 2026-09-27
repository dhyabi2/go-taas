// Package auth implements the authentication and authorization service:
// local accounts, API keys, SSO federation, and the gateway-facing key
// verification used to authenticate inference traffic.
//
// Compose seeding (feature-22 AD6/AD10): the auth module's Migrate hook
// seeds the Keycloak OIDC provider row, the admin platform user, and the
// admin user's identity binding idempotently, so the compose stack is
// verifiable end to end without manual configuration.
package auth

import (
	"context"

	"github.com/google/uuid"

	apierrors "github.com/go-taas/go-taas/pkg/errors"
)

// seedKeycloakProviderID is the provider id of the compose Keycloak IdP.
const seedKeycloakProviderID = "keycloak"

// seedKeycloakIssuer is the issuer of the compose Keycloak realm. It is
// reachable from both the host browser and the taas-server container
// (the compose network resolves `keycloak` to the container).
const seedKeycloakIssuer = "http://keycloak:8080/realms/go-taas"

// seedKeycloakClientID is the confidential client of the compose realm.
const seedKeycloakClientID = "go-taas-console"

// seedKeycloakClientSecret is the client secret of the compose realm.
// It exists only in the compose realm-export.json and this seed; the
// design doc §9 states seeded credentials are compose-only and
// production deployments configure their own IdP.
//nolint:gosec // compose-only credential, never used in production
const seedKeycloakClientSecret = "go-taas-console-secret"

// seedKeycloakRedirectURI is the redirect URI of the compose client.
const seedKeycloakRedirectURI = "http://localhost:9091/api/v1/auth/sso/*"

// seedAdminUsername is the compose admin user's username.
const seedAdminUsername = "admin"

// seedKeycloakAttributeMapping maps the Keycloak admin role claim to the
// TaaS admin role (feature-22 §5.4). The role claim is the nested
// realm_access.roles array, flattened by decodeIDTokenClaims.
const seedKeycloakAttributeMapping = `{"username":"preferred_username","email":"email","role":"realm_access.roles"}`

// seedComposeProviderAndAdmin seeds the Keycloak provider, the admin
// user, and the admin user's identity binding idempotently (feature-22
// AD6/AD10). Each step checks existence before inserting, so re-running
// compose startup does not duplicate the provider, the user, or the
// binding (FR4.4).
func (s *Service) seedComposeProviderAndAdmin(ctx context.Context) error {
	repo, err := s.ssoRepository()
	if err != nil {
		return err
	}

	// 1. The Keycloak OIDC provider row.
	if _, err := repo.FindProvider(ctx, seedKeycloakProviderID); isRecordNotFound(err) {
		prov := &SSOProvider{
			ID:                 seedKeycloakProviderID,
			Type:               ProviderTypeOIDC,
			DisplayName:        "Keycloak",
			Issuer:             seedKeycloakIssuer,
			ClientID:           seedKeycloakClientID,
			ClientSecret:       seedKeycloakClientSecret,
			RedirectURI:        seedKeycloakRedirectURI,
			Scopes:             "openid profile email",
			Enabled:            true,
			AllowAutoProvision: true,
			AttributeMapping:   seedKeycloakAttributeMapping,
		}
		if err := repo.CreateProvider(ctx, prov); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}

	// 2. The admin platform user.
	adminUser, err := repo.FindUserByUsername(ctx, seedAdminUsername)
	if isRecordNotFound(err) {
		adminUser = &User{
			ID:       uuid.NewString(),
			Username: seedAdminUsername,
			Email:    "admin@example.com",
		}
		if err := repo.CreateUser(ctx, adminUser); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}

	// 3. The admin user's identity binding. The external subject is
	// issuer + ":" + the Keycloak admin subject. The realm-export.json
	// does not fix the subject, so the seed resolves it from the IdP at
	// first boot; if it cannot be resolved, the binding is created on
	// the first successful login via JIT provisioning (AD10).
	prov, err := repo.FindProvider(ctx, seedKeycloakProviderID)
	if err != nil {
		return err
	}
	adminSubject, err := s.resolveKeycloakAdminSubject(ctx, prov)
	if err != nil || adminSubject == "" {
		// Best-effort: JIT provisioning covers the binding on first
		// login. The provider and user are already seeded.
		return nil
	}
	externalSubject := prov.Issuer + ":" + adminSubject
	if _, err := repo.FindBindingBySubject(ctx, seedKeycloakProviderID, externalSubject); isRecordNotFound(err) {
		binding := &IdentityBinding{
			ID:              uuid.NewString(),
			ProviderID:      seedKeycloakProviderID,
			ExternalSubject: externalSubject,
			UserID:          adminUser.ID,
		}
		if err := repo.CreateBinding(ctx, binding); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	return nil
}

// resolveKeycloakAdminSubject resolves the Keycloak admin user's subject
// from the IdP at first boot. The realm-export.json does not fix the
// subject, so the seed reads it from the IdP's admin user. A failure
// returns ("", nil) so the caller falls back to JIT provisioning.
func (s *Service) resolveKeycloakAdminSubject(ctx context.Context, prov *SSOProvider) (string, error) {
	plugin, err := s.pluginFor(prov.Type)
	if err != nil {
		return "", err
	}
	// The admin user's credentials are the compose defaults. The
	// password grant returns the identity with the subject claim. The
	// external subject prefix is the provider's actual issuer.
	identity, err := plugin.PasswordGrant(ctx, prov, seedAdminUsername, seedAdminUsername)
	if err != nil {
		return "", nil // best-effort: JIT covers the binding
	}
	return subjectFromExternal(identity.ExternalSubject, prov.Issuer), nil
}

// subjectFromExternal extracts the subject from an external subject of
// the form "issuer:subject".
func subjectFromExternal(external, issuer string) string {
	prefix := issuer + ":"
	if len(external) > len(prefix) && external[:len(prefix)] == prefix {
		return external[len(prefix):]
	}
	return ""
}

// EnsureComposeSeed runs the compose provider/admin seeding using the
// service's wired repository. It is the injection point for tests and
// FVT, which construct the service directly instead of running the full
// Migrate hook.
func (s *Service) EnsureComposeSeed(ctx context.Context) error {
	if s.ssoRepo == nil {
		return apierrors.Newf(apierrors.CodeInternal, "auth: sso repository not wired")
	}
	return s.seedComposeProviderAndAdmin(ctx)
}